package engine

import (
	"github.com/bob2325168/spider/collect"
	"github.com/bob2325168/spider/parse/douban"
	"github.com/bob2325168/spider/storage"
	"github.com/robertkrimen/otto"
	"go.uber.org/zap"
	"runtime/debug"
	"sync"
)

var Store = &CrawlerStore{
	list: []*collect.Task{},
	Hash: map[string]*collect.Task{},
}

type Crawler struct {
	out         chan collect.ParseResult
	Visited     map[string]bool
	VisitedLock sync.Mutex

	failures    map[string]*collect.Request
	failureLock sync.Mutex

	options
}

type Scheduler interface {
	Schedule()
	Push(...*collect.Request)
	Pull() *collect.Request
}

// Schedule 调度引擎
type Schedule struct {
	requestCh   chan *collect.Request
	workerCh    chan *collect.Request
	reqQueue    []*collect.Request
	priReqQueue []*collect.Request
	Logger      *zap.Logger
}

// Config 配置选项
type Config struct {
	WorkCount int
	Fetcher   collect.Fetcher
	Logger    *zap.Logger
	Seeds     []*collect.Request
}

type CrawlerStore struct {
	list []*collect.Task
	Hash map[string]*collect.Task
}

func init() {
	Store.Add(douban.DoubangroupTask)
	Store.AddJsTask(douban.DoubangroupJsTask)
	Store.Add(douban.DoubanBookTask)
}

func (c *CrawlerStore) Add(task *collect.Task) {
	c.Hash[task.Name] = task
	c.list = append(c.list, task)
}

// AddJsReq 用于动态规则添加请求
func AddJsReq(jreq map[string]interface{}) []*collect.Request {
	reqs := make([]*collect.Request, 0)
	req := &collect.Request{}
	u, ok := jreq["Url"].(string)
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

func AddJsReqs(jsreqs []map[string]interface{}) []*collect.Request {

	reqs := make([]*collect.Request, 0)

	for _, jreq := range jsreqs {
		req := &collect.Request{}
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

func (c *CrawlerStore) AddJsTask(m *collect.TaskModule) {

	task := &collect.Task{
		Property: m.Property,
	}

	task.Rule.Root = func() ([]*collect.Request, error) {
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

		return e.([]*collect.Request), nil
	}

	for _, r := range m.Rules {
		parseFunc := func(parse string) func(ctx *collect.Context) (collect.ParseResult, error) {
			return func(ctx *collect.Context) (collect.ParseResult, error) {
				vm := otto.New()
				if err := vm.Set("ctx", ctx); err != nil {
					return collect.ParseResult{}, err
				}

				v, err := vm.Eval(parse)
				if err != nil {
					return collect.ParseResult{}, err
				}

				e, err := v.Export()
				if err != nil {
					return collect.ParseResult{}, err
				}

				return e.(collect.ParseResult), err
			}
		}(r.ParseFunc)

		if task.Rule.Trunk == nil {
			task.Rule.Trunk = make(map[string]*collect.Rule, 0)
		}
		task.Rule.Trunk[r.Name] = &collect.Rule{ParseFunc: parseFunc}
	}

	c.Hash[task.Name] = task
	c.list = append(c.list, task)
}

func NewEngine(opts ...Option) *Crawler {
	options := defaultOptions
	for _, opt := range opts {
		opt(&options)
	}
	e := &Crawler{}
	e.Visited = make(map[string]bool, 100)
	e.out = make(chan collect.ParseResult)
	e.failures = make(map[string]*collect.Request)
	e.options = options
	return e
}

func NewSchedule() *Schedule {
	s := &Schedule{}
	requestCh := make(chan *collect.Request)
	workerCh := make(chan *collect.Request)
	s.requestCh = requestCh
	s.workerCh = workerCh
	return s
}

func (e *Crawler) Run() {
	go e.Schedule()
	for i := 0; i < e.WorkCount; i++ {
		go e.CreateWork()
	}
	e.HandleResult()
}

func (s *Schedule) Schedule() {

	var req *collect.Request
	var ch chan *collect.Request

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

func (s *Schedule) Push(reqs ...*collect.Request) {
	for _, req := range reqs {
		s.requestCh <- req
	}
}

func (s *Schedule) Pull() *collect.Request {
	return <-s.workerCh
}

func (e *Crawler) Schedule() {

	var reqs []*collect.Request
	for _, seed := range e.Seeds {
		task := Store.Hash[seed.Name]
		task.Fetcher = seed.Fetcher
		task.Storage = seed.Storage
		task.Logger = e.Logger
		task.Limiter = seed.Limiter
		rootReqs, err := task.Rule.Root()
		if err != nil {
			e.Logger.Error("get root failed", zap.Error(err))
			continue
		}
		for _, req := range rootReqs {
			req.Task = task
		}
		reqs = append(reqs, rootReqs...)
	}
	go e.scheduler.Schedule()
	go e.scheduler.Push(reqs...)
}

func (e *Crawler) CreateWork() {
	// 考虑到panic会发生，需要recover
	defer func() {
		if err := recover(); err != nil {
			e.Logger.Error("worker panic",
				zap.Any("err", err),
				zap.String("stack", string(debug.Stack())),
			)
		}
	}()
	for {
		r := e.scheduler.Pull()
		if err := r.Check(); err != nil {
			e.Logger.Error("check failed", zap.Error(err))
			continue
		}
		// 检测是否之前访问过以及是否可以重复爬取
		if e.HasVisited(r) && !r.Task.Reload {
			e.Logger.Debug("request has visited", zap.String("url", r.URL))
			continue
		}
		e.StoreVisitedInfo(r)

		//请求真实的URL
		body, err := r.Fetch()
		if err != nil {
			e.Logger.Error("fetch failed ",
				zap.Error(err),
				zap.String("url", r.URL),
			)
			//处理失败
			e.HandleFailure(r)
			continue
		}
		if len(body) < 6000 {
			e.Logger.Error("fetch failed ",
				zap.Int("length", len(body)),
				zap.String("url", r.URL),
			)
			e.HandleFailure(r)
			continue
		}

		rule := r.Task.Rule.Trunk[r.RuleName]
		result, err := rule.ParseFunc(
			&collect.Context{
				Req:  r,
				Body: body,
			},
		)
		if err != nil {
			e.Logger.Error("parseFunc failed", zap.Error(err), zap.String("url", r.URL))
			continue
		}

		if len(result.Requests) > 0 {
			go e.scheduler.Push(result.Requests...)
		}

		e.out <- result
	}

}

func (e *Crawler) HandleResult() {
	for {
		select {
		case result := <-e.out:
			for _, item := range result.Items {
				//存入sql
				switch d := item.(type) {
				case *storage.DataCell:
					name := d.GetTableName()
					task := Store.Hash[name]
					err := task.Storage.Save(d)
					if err != nil {
						e.Logger.Error("save data error", zap.Error(err))
					}
				}
				e.Logger.Sugar().Info("get result: ", item)
			}
		}
	}
}

func (e *Crawler) HasVisited(r *collect.Request) bool {
	e.VisitedLock.Lock()
	defer e.VisitedLock.Unlock()
	unique := r.Unique()
	return e.Visited[unique]
}

func (e *Crawler) StoreVisitedInfo(reqs ...*collect.Request) {
	e.VisitedLock.Lock()
	defer e.VisitedLock.Unlock()

	for _, r := range reqs {
		unique := r.Unique()
		e.Visited[unique] = true
	}
}

func (e *Crawler) HandleFailure(req *collect.Request) {

	if !req.Task.Reload {
		e.VisitedLock.Lock()
		unique := req.Unique()
		delete(e.Visited, unique)
		e.VisitedLock.Unlock()
	}
	e.failureLock.Lock()
	defer e.failureLock.Unlock()
	if _, ok := e.failures[req.Unique()]; !ok {
		e.failures[req.Unique()] = req
		e.scheduler.Push(req)
	}
}

func GetFields(taskName string, ruleName string) []string {
	return Store.Hash[taskName].Rule.Trunk[ruleName].ItemFields
}
