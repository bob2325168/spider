package engine

import (
	"context"
	"fmt"
	"github.com/bob2325168/spider/master"
	"github.com/bob2325168/spider/parse/douban"
	"github.com/bob2325168/spider/parse/doubangroup"
	"github.com/bob2325168/spider/parse/doubangroupjs"
	"github.com/bob2325168/spider/spider"
	"github.com/robertkrimen/otto"
	clientV3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"runtime/debug"
	"strings"
	"sync"
)

var Store = &CrawlerStore{
	list: []*spider.Task{},
	Hash: map[string]*spider.Task{},
}

type Crawler struct {
	id          string
	out         chan spider.ParseResult
	Visited     map[string]bool
	VisitedLock sync.Mutex
	rLock       sync.Mutex
	resources   map[string]*master.ResourceSpec

	// 失败请求id ==> 失败请求
	failures    map[string]*spider.Request
	failureLock sync.Mutex

	etcdCli *clientV3.Client

	options
}

type Scheduler interface {
	Schedule()
	Push(...*spider.Request)
	Pull() *spider.Request
}

// Schedule 调度引擎
type Schedule struct {
	requestCh   chan *spider.Request
	workerCh    chan *spider.Request
	reqQueue    []*spider.Request
	priReqQueue []*spider.Request
	Logger      *zap.Logger
}

// Config 配置选项
type Config struct {
	WorkCount int
	Fetcher   spider.Fetcher
	Logger    *zap.Logger
	Seeds     []*spider.Request
}

type CrawlerStore struct {
	list []*spider.Task
	Hash map[string]*spider.Task
}

func init() {
	Store.Add(doubangroup.GroupTask)
	Store.Add(douban.BookTask)
	Store.AddJsTask(doubangroupjs.GroupJSTask)
}

func (c *CrawlerStore) Add(task *spider.Task) {
	c.Hash[task.Name] = task
	c.list = append(c.list, task)
}

// AddJsReq 用于动态规则添加请求。
func AddJsReq(jreq map[string]interface{}) []*spider.Request {

	reqs := make([]*spider.Request, 0)
	req := &spider.Request{}
	u, ok := jreq["URL"].(string)
	if !ok {
		return nil
	}

	req.URL = u
	req.RuleName, _ = jreq["RuleName"].(string)
	req.Method, _ = jreq["Method"].(string)
	req.Priority, _ = jreq["Priority"].(int64)
	reqs = append(reqs, req)

	return reqs
}

func AddJsReqs(jsreqs []map[string]interface{}) []*spider.Request {

	reqs := make([]*spider.Request, 0)
	for _, jreq := range jsreqs {
		req := &spider.Request{}
		u, ok := jreq["URL"].(string)
		if !ok {
			return nil
		}
		req.URL = u
		req.RuleName, _ = jreq["RuleName"].(string)
		req.Method, _ = jreq["Method"].(string)
		req.Priority, _ = jreq["Priority"].(int64)
		reqs = append(reqs, req)
	}
	return reqs
}

func (c *CrawlerStore) AddJsTask(m *spider.TaskModule) {

	task := &spider.Task{}
	task.Rule.Root = func() ([]*spider.Request, error) {
		vm := otto.New()
		if err := vm.Set("AddJsReq", AddJsReqs); err != nil {
			return nil, err
		}

		v, err := vm.Eval(m.Root)
		if err != nil {
			return nil, err
		}

		e, err := v.Export()
		if err != nil {
			return nil, err
		}

		return e.([]*spider.Request), nil
	}

	for _, r := range m.Rules {
		parseFunc := func(parse string) func(ctx *spider.Context) (spider.ParseResult, error) {
			return func(ctx *spider.Context) (spider.ParseResult, error) {

				vm := otto.New()
				if err := vm.Set("ctx", ctx); err != nil {
					return spider.ParseResult{}, err
				}

				v, err := vm.Eval(parse)
				if err != nil {
					return spider.ParseResult{}, err
				}

				e, err := v.Export()
				if err != nil {
					return spider.ParseResult{}, err
				}

				return e.(spider.ParseResult), err
			}
		}(r.ParseFunc)

		if task.Rule.Trunk == nil {
			task.Rule.Trunk = make(map[string]*spider.Rule, 0)
		}
		task.Rule.Trunk[r.Name] = &spider.Rule{ParseFunc: parseFunc}
	}

	c.Hash[task.Name] = task
	c.list = append(c.list, task)
}

func NewEngine(opts ...Option) (*Crawler, error) {

	options := defaultOptions
	for _, opt := range opts {
		opt(&options)
	}

	c := &Crawler{}
	c.Visited = make(map[string]bool, 100)
	c.out = make(chan spider.ParseResult)
	c.failures = make(map[string]*spider.Request)
	c.options = options

	// 任务加上默认的采集器和存储器
	for _, task := range Store.list {
		task.Fetcher = c.Fetcher
		task.Storage = c.Storage

	}

	endpoints := []string{c.RegistryUrl}
	cli, err := clientV3.New(clientV3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}

	c.etcdCli = cli

	return c, nil
}

func NewSchedule() *Schedule {
	s := &Schedule{}
	requestCh := make(chan *spider.Request)
	workerCh := make(chan *spider.Request)
	s.requestCh = requestCh
	s.workerCh = workerCh

	return s
}

func (c *Crawler) Run(id string, cluster bool) {

	c.id = id
	if !cluster {
		c.handleSeeds()
	}

	// 加载全量资源
	go c.loadResource()

	// 监控资源
	go c.watchResource()

	// 调度任务
	go c.Schedule()

	for i := 0; i < c.WorkCount; i++ {
		go c.CreateWork()
	}

	// 处理返回的结果
	c.HandleResult()
}

// watchResource 借助etcd的watch机制，监听资源的变化。当etcd中的/resources/目录下的资源发生变化时，会触发watch事件。
// 通过watch事件，可以实现动态的资源管理，比如新增、删除、修改资源。如果是新增事件，会将新增的资源加入到调度器中，调用runTask方法执行任务。
func (c *Crawler) watchResource() {

	watchCh := c.etcdCli.Watch(context.Background(), master.RESOURCE_PATH, clientV3.WithPrefix(), clientV3.WithPrevKV())

	for w := range watchCh {

		if w.Err() != nil {
			c.Logger.Error("watch resource failed, err:%v", zap.Error(w.Err()))
			continue
		}

		if w.Canceled {
			c.Logger.Info("watch resource canceled")
			return
		}

		for _, ev := range w.Events {

			switch ev.Type {
			// 新增资源
			case clientV3.EventTypePut:
				spec, err := master.Decode(ev.Kv.Value)
				if err != nil {
					c.Logger.Error("decode etcd value failed", zap.Error(err))
				}
				if ev.IsCreate() {
					c.Logger.Info("add resource", zap.Any("spec", spec))
				} else if ev.IsModify() {
					c.Logger.Info("update resource", zap.Any("spec", spec))
				}
				c.rLock.Lock()
				c.runTasks(spec.Name)
				c.rLock.Unlock()

			// 删除资源
			case clientV3.EventTypeDelete:
				spec, err := master.Decode(ev.PrevKv.Value)
				if err != nil {
					c.Logger.Error("decode etcd value failed", zap.Error(err))
				}
				c.Logger.Info("delete resource", zap.Any("spec", spec))
				c.rLock.Lock()
				c.deleteTasks(spec.Name)
				c.rLock.Unlock()

			}

		}

	}
}

func (s *Schedule) Schedule() {

	var req *spider.Request
	var ch chan *spider.Request

	for {
		if req == nil && len(s.priReqQueue) > 0 {
			req = s.priReqQueue[0]
			s.priReqQueue = s.priReqQueue[1:]
			ch = s.workerCh
		}

		if req == nil && len(s.reqQueue) > 0 {
			req = s.reqQueue[0]
			s.reqQueue = s.reqQueue[1:]
			ch = s.workerCh
		}

		// 请求校验
		if req != nil {
			if err := req.Check(); err != nil {
				zap.L().Debug("check failed", zap.Error(err))
			}
			req = nil
			ch = nil
			continue
		}

		select {
		case r := <-s.requestCh:
			if r.Priority > 0 {
				s.priReqQueue = append(s.priReqQueue, r)
			} else {
				s.reqQueue = append(s.reqQueue, r)
			}

		case ch <- req:
			req = nil
			ch = nil
		}
	}
}

func (s *Schedule) Push(reqs ...*spider.Request) {
	for _, req := range reqs {
		s.requestCh <- req
	}
}

func (s *Schedule) Pull() *spider.Request {
	return <-s.workerCh
}

func (c *Crawler) Schedule() {
	go c.scheduler.Schedule()
}

func (c *Crawler) CreateWork() {

	// 考虑到panic会发生，需要recover
	defer func() {
		if err := recover(); err != nil {
			c.Logger.Error("worker panic",
				zap.Any("err", err),
				zap.String("stack", string(debug.Stack())),
			)
		}
	}()

	for {
		r := c.scheduler.Pull()
		if err := r.Check(); err != nil {
			c.Logger.Error("check failed", zap.Error(err))
			continue
		}
		// 检测是否之前访问过以及是否可以重复爬取
		if c.HasVisited(r) && !r.Task.Reload {
			c.Logger.Debug("request has visited", zap.String("url", r.URL))
			continue
		}
		// 标识已来访
		c.StoreVisited(r)

		//请求真实的URL
		body, err := r.Fetch()
		if err != nil {
			c.Logger.Error("fetch failed ", zap.Error(err), zap.String("url", r.URL))
			//处理失败
			c.HandleFailure(r)
			continue
		}

		if len(body) < 6000 {
			c.Logger.Error("fetch failed ", zap.Int("length", len(body)), zap.String("url", r.URL))
			c.HandleFailure(r)
			continue
		}

		rule := r.Task.Rule.Trunk[r.RuleName]
		result, err := rule.ParseFunc(
			&spider.Context{
				Req:  r,
				Body: body,
			},
		)
		if err != nil {
			c.Logger.Error("parseFunc failed", zap.Error(err), zap.String("url", r.URL))
			continue
		}

		if len(result.Requests) > 0 {
			go c.scheduler.Push(result.Requests...)
		}

		c.out <- result
	}
}

func (c *Crawler) HandleResult() {

	for result := range c.out {
		for _, item := range result.Items {
			switch d := item.(type) {
			case *spider.DataCell:
				if err := d.Task.Storage.Save(d); err != nil {
					c.Logger.Error("save data error", zap.Error(err))
				}
			}
			c.Logger.Sugar().Info("get result: ", item)
		}
	}
}

func (c *Crawler) HasVisited(r *spider.Request) bool {
	c.VisitedLock.Lock()
	defer c.VisitedLock.Unlock()
	unique := r.Unique()
	return c.Visited[unique]
}

func (c *Crawler) StoreVisited(reqs ...*spider.Request) {
	c.VisitedLock.Lock()
	defer c.VisitedLock.Unlock()

	for _, r := range reqs {
		unique := r.Unique()
		c.Visited[unique] = true
	}
}

func (c *Crawler) HandleFailure(req *spider.Request) {

	if !req.Task.Reload {
		c.VisitedLock.Lock()
		unique := req.Unique()
		delete(c.Visited, unique)
		c.VisitedLock.Unlock()
	}

	c.failureLock.Lock()
	defer c.failureLock.Unlock()

	if _, ok := c.failures[req.Unique()]; !ok {
		// 首次失败时，重新执行一次
		c.failures[req.Unique()] = req
		c.scheduler.Push(req)
	}
}

func (c *Crawler) handleSeeds() {

	var reqs []*spider.Request
	for _, task := range c.Seeds {
		t, ok := Store.Hash[task.Name]
		if !ok {
			c.Logger.Error("can not find preset tasks", zap.String("task name", task.Name))
			continue
		}
		task.Rule = t.Rule // 任务规则
		rootReqs, err := task.Rule.Root()
		if err != nil {
			c.Logger.Error("get root failed", zap.Error(err))
			continue
		}

		for _, req := range rootReqs {
			req.Task = task
		}

		reqs = append(reqs, rootReqs...)
	}

	go c.scheduler.Push(reqs...)
}

func (c *Crawler) loadResource() error {

	resp, err := c.etcdCli.Get(context.Background(), master.RESOURCE_PATH, clientV3.WithPrefix(), clientV3.WithSerializable())
	if err != nil {
		return fmt.Errorf("etcd get failed")
	}

	resources := make(map[string]*master.ResourceSpec)
	for _, kv := range resp.Kvs {
		r, err := master.Decode(kv.Value)
		if err == nil && r != nil {
			id := getID(r.AssignedNode)
			if len(id) > 0 && c.id == id {
				resources[r.Name] = r
			}
		}
	}
	c.Logger.Info("leader init load resource", zap.Int("lenth", len(resources)))

	c.rLock.Lock()
	defer c.rLock.Unlock()

	c.resources = resources
	for _, r := range c.resources {
		c.runTasks(r.Name)
	}

	return nil
}

// 通过任务名称从全局任务池中获取爬虫任务，调用task的rule的root方法获取初始请求，并将请求放入调度器执行
func (c *Crawler) runTasks(taskName string) {

	t, ok := Store.Hash[taskName]
	if !ok {
		c.Logger.Error("can not find preset tasks", zap.String("task taskName", taskName))
		return
	}
	res, err := t.Rule.Root()
	if err != nil {
		c.Logger.Error("get root failed", zap.Error(err))
		return
	}
	for _, r := range res {
		r.Task = t
	}
	go c.scheduler.Push(res...)
}

func (c *Crawler) deleteTasks(taskName string) {

	t, ok := Store.Hash[taskName]
	if !ok {
		c.Logger.Error("cannot find task", zap.String("task name:", taskName))
		return
	}
	// 标识该任务对应的资源已经删除，任务停止执行
	t.Closed = true
	delete(c.resources, taskName)
}

func getID(node string) string {
	s := strings.Split(node, "|")
	if len(s) < 2 {
		return ""
	}
	return s[0]
}

func GetFields(taskName string, ruleName string) []string {
	return Store.Hash[taskName].Rule.Trunk[ruleName].ItemFields
}
