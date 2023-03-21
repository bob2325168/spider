package master

import (
	"context"
	"encoding/json"
	"fmt"
	proto "github.com/bob2325168/spider/proto/crawler"
	"github.com/bwmarrin/snowflake"
	"github.com/go-acme/lego/v4/log"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"go-micro.dev/v4/client"
	"go-micro.dev/v4/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.uber.org/zap"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var serviceName = "go.micro.server.worker"

type Command int

const (
	MSGADD Command = iota
	MSGDELETE
)

const (
	RESOURCE_PATH = "/resources"
)

type Master struct {
	Id          string
	ready       int32
	leaderId    string
	workerNodes map[string]*NodeSpec
	resources   map[string]*ResourceSpec
	IdGen       *snowflake.Node
	etcdCli     *clientv3.Client
	farwardCli  proto.CrawlerMasterService

	rLock sync.Mutex

	options
}

type Message struct {
	Cmd   Command
	Specs []*ResourceSpec
}

type ResourceSpec struct {
	Id           string
	Name         string
	AssignedNode string
	CreatedTime  int64
}

type NodeSpec struct {
	Node    *registry.Node
	Payload int // 标识当前节点的负载
}

func New(id string, opts ...Option) (*Master, error) {

	m := &Master{}
	options := defaultOptions
	for _, opt := range opts {
		opt(&options)
	}
	m.options = options
	m.resources = make(map[string]*ResourceSpec)

	//使用snowflake算法生成id
	node, err := snowflake.NewNode(1)
	if err != nil {
		return nil, err
	}
	m.IdGen = node

	//获取本地服务的IP地址
	ipv4, err := getLocalIP()
	if err != nil {
		return nil, err
	}

	// 生成masterId，全局唯一
	m.Id = getMasterId(id, ipv4, m.GRPCAddress)
	m.logger.Sugar().Debugln("master-id", m.Id)

	endpoints := []string{m.registryURL}
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}
	m.etcdCli = cli

	// 监听worker的变化以及安排任务
	m.updateWorkerNodes()

	// 添加爬虫资源
	m.addSeed()

	// 启动协程竞选Master
	go m.campaign()

	// 启动协程处理消息
	go m.handleMessages()

	return m, nil
}

/*
资源管理
*/
func (m *Master) AddResource(ctx context.Context, req *proto.ResourceSpec, resp *proto.NodeSpec) error {

	// 如果不是leader，就获取leader的地址，完成转发请求
	//  不指定leader的地址，随机转发
	if !m.isLeader() && m.leaderId != "" && m.leaderId != m.Id {
		addr := getLeaderAddress(m.leaderId)
		nodeSpec, err := m.farwardCli.AddResource(ctx, req, client.WithAddress(addr))
		if nodeSpec != nil {
			resp.Id = nodeSpec.Id
			resp.Address = nodeSpec.Address
		}
		return err
	}

	// 如果是leader，就直接处理请求
	m.rLock.Lock()
	defer m.rLock.Unlock()

	nodeSpec, err := m.addResource(&ResourceSpec{Name: req.Name})
	if nodeSpec != nil {
		resp.Id = nodeSpec.Node.Id
		resp.Address = nodeSpec.Node.Address
	}
	return err
}

func getLeaderAddress(leaderId string) string {

	s := strings.Split(leaderId, "-")
	if len(s) < 2 {
		return ""
	}
	return s[1]
}

func (m *Master) DeleteResource(ctx context.Context, spec *proto.ResourceSpec, empty *empty.Empty) error {

	if !m.isLeader() && m.leaderId != "" && m.leaderId != m.Id {
		addr := getLeaderAddress(m.leaderId)
		_, err := m.farwardCli.DeleteResource(ctx, spec, client.WithAddress(addr))
		return err
	}

	m.rLock.Lock()
	defer m.rLock.Unlock()

	// 查找资源是否已经存在
	r, ok := m.resources[spec.Name]
	if !ok {
		return errors.New("no such task")
	}

	if _, err := m.etcdCli.Delete(context.Background(), getResourcePath(spec.Name)); err != nil {
		return err
	}

	// 删除资源
	delete(m.resources, spec.Name)

	if r.AssignedNode != "" {

		nodeId, err := getNodeId(r.AssignedNode)
		if err != nil {
			return err
		}

		if ns, ok := m.workerNodes[nodeId]; ok {
			ns.Payload--
		}
	}

	return nil
}

func (m *Master) campaign() {

	sess, err := concurrency.NewSession(m.etcdCli, concurrency.WithTTL(5))
	if err != nil {
		log.Println("NewSession", "error", "err", err)
	}
	defer sess.Close()

	// 创建一个新的etcd选举
	e := concurrency.NewElection(sess, "/resources/election")
	leaderCh := make(chan error)
	go m.elect(e, leaderCh)

	// 监听leader的变化
	leaderChange := e.Observe(context.Background())

	select {
	case resp := <-leaderChange:
		m.logger.Info("watch leader change", zap.String("leader", string(resp.Kvs[0].Value)))
	}
	workerNodeChange := m.watchWorker()

	for {
		select {
		// 监听当前master是否成为了leader
		case err := <-leaderCh:
			if err != nil {
				m.logger.Error("leader elect failed", zap.Error(err))
				go m.elect(e, leaderCh)
			} else {
				m.logger.Info("master change to leader")
				m.leaderId = m.Id
				if !m.isLeader() {
					if err := m.selectedForLeader(); err != nil {
						m.logger.Error("become leader failed", zap.Error(err))
					}
				}
			}

		// 监听当前集群中leader是否发生了变化
		case resp := <-leaderChange:

			if len(resp.Kvs) > 0 {
				m.logger.Info("watch leader change", zap.String("leader:", string(resp.Kvs[0].Value)))
			}

		// 监听worker节点是否发生变化
		case resp := <-workerNodeChange:

			m.logger.Info("watch worker change", zap.Any("worker:", resp))
			m.updateWorkerNodes()

			if err := m.loadResource(); err != nil {
				m.logger.Error("loadResource failed:%w", zap.Error(err))
			}

			m.reAssign()

		case <-time.After(20 * time.Second):

			resp, err := e.Leader(context.Background())
			if err != nil {
				m.logger.Info("get leader failed", zap.Error(err))
				if errors.Is(err, concurrency.ErrElectionNoLeader) {
					go m.elect(e, leaderCh)
				}
			}
			if resp != nil && len(resp.Kvs) > 0 {
				m.logger.Debug("get leader", zap.String("value", string(resp.Kvs[0].Value)))
				if m.isLeader() && m.Id != string(resp.Kvs[0].Value) {
					atomic.StoreInt32(&m.ready, 0)
				}
			}
		}
	}
}

func (m *Master) isLeader() bool {
	return atomic.LoadInt32(&m.ready) != 0
}

func (m *Master) elect(e *concurrency.Election, ch chan error) {
	// 一直阻塞，直到选举成功
	err := e.Campaign(context.Background(), m.Id)
	ch <- err
}

func (m *Master) watchWorker() chan *registry.Result {

	watch, err := m.registry.Watch(registry.WatchService(serviceName))
	if err != nil {
		panic(err)
	}

	ch := make(chan *registry.Result)
	go func() {
		for {
			res, err := watch.Next()
			if err != nil {
				m.logger.Info("watch worker service failed", zap.Error(err))
				continue
			}
			ch <- res
		}
	}()
	return ch
}

// 当Master成为leader之后，要全量获取一次etcd中的最新资源信息，并把他保存到内存中
func (m *Master) selectedForLeader() error {

	// 更新当前的worker节点
	m.updateWorkerNodes()

	// 全量加载爬虫资源
	if err := m.loadResource(); err != nil {
		return fmt.Errorf("loadResource failed:%w", err)
	}

	// 重新分配资源
	m.reAssign()

	atomic.StoreInt32(&m.ready, 1)
	return nil
}

func (m *Master) updateWorkerNodes() {

	services, err := m.registry.GetService(serviceName)
	if err != nil {
		m.logger.Error("get service", zap.Error(err))
	}

	m.rLock.Lock()
	defer m.rLock.Unlock()

	nodes := make(map[string]*NodeSpec)
	if len(services) > 0 {
		for _, spec := range services[0].Nodes {
			nodes[spec.Id] = &NodeSpec{Node: spec}
		}
	}

	added, deleted, changed := workerNodeDiff(m.workerNodes, nodes)
	m.logger.Sugar().Info("worker joined: ", added, ", leaved: ", deleted, ", changed: ", changed)

	m.workerNodes = nodes
}

func (m *Master) addSeed() {

	rs := make([]*ResourceSpec, 0, len(m.Seeds))

	for _, seed := range m.Seeds {
		resp, err := m.etcdCli.Get(context.Background(), getResourcePath(seed.Name), clientv3.WithSerializable())
		if err != nil {
			m.logger.Error("etcd get failed", zap.Error(err))
			continue
		}

		if len(resp.Kvs) == 0 {
			r := &ResourceSpec{Name: seed.Name}
			rs = append(rs, r)
		}
	}

	m.AddResources(rs)
}

func getResourcePath(name string) string {
	return fmt.Sprintf("%s/%s", RESOURCE_PATH, name)
}

func (m *Master) handleMessages() {

	msgCh := make(chan *Message)

	select {
	case msg := <-msgCh:
		switch msg.Cmd {
		case MSGADD:
			m.AddResources(msg.Specs)
		}
	}
}

func (m *Master) AddResources(rs []*ResourceSpec) {
	for _, r := range rs {
		m.addResource(r)
	}
}

func (m *Master) addResource(r *ResourceSpec) (*NodeSpec, error) {

	// 使用snowflake算法，生成一个单调递增的分布式id
	r.Id = m.IdGen.Generate().String()

	ns, err := m.assign(r)
	if err != nil {
		m.logger.Error("assign failed", zap.Error(err))
		return nil, err
	}

	if ns.Node == nil {
		m.logger.Error("no node to assign")
		return nil, err
	}

	r.AssignedNode = ns.Node.Id + "|" + ns.Node.Address
	r.CreatedTime = time.Now().UnixNano()
	m.logger.Debug("add resource", zap.Any("specs", r))

	_, err = m.etcdCli.Put(context.Background(), getResourcePath(r.Name), encode(r))
	if err != nil {
		m.logger.Error("put etcd failed", zap.Error(err))
		return nil, err
	}

	m.resources[r.Name] = r

	// 当前节点负载递增
	ns.Payload++

	return ns, nil

}

func encode(r *ResourceSpec) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func Decode(ds []byte) (*ResourceSpec, error) {
	var r *ResourceSpec
	err := json.Unmarshal(ds, &r)
	return r, err
}

// 给worker分配任务
func (m *Master) assign(r *ResourceSpec) (*NodeSpec, error) {

	// 候选人
	candidates := make([]*NodeSpec, 0, len(m.workerNodes))

	for _, node := range m.workerNodes {
		candidates = append(candidates, node)
	}

	// 找到最低负载
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Payload < candidates[j].Payload
	})

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return nil, errors.New("no worker nodes")
}

// 全量加载当前的爬虫资源
func (m *Master) loadResource() error {

	resp, err := m.etcdCli.Get(context.Background(), RESOURCE_PATH, clientv3.WithSerializable())
	if err != nil {
		return fmt.Errorf("etcd get failed")
	}

	resources := make(map[string]*ResourceSpec)
	for _, kv := range resp.Kvs {
		r, err := Decode(kv.Value)
		if err != nil && r != nil {
			resources[r.Name] = r
		}
	}

	m.logger.Info("leader init load resource", zap.Int("lenth", len(m.resources)))

	m.rLock.Lock()
	defer m.rLock.Unlock()

	m.resources = resources

	for _, r := range m.resources {
		if r.AssignedNode != "" {
			id, err := getNodeId(r.AssignedNode)
			if err != nil {
				m.logger.Error("get nodeId failed", zap.Error(err))
			}
			if node, ok := m.workerNodes[id]; ok {
				node.Payload++
			}
		}
	}
	return nil
}

/*
当资源还没有分配worker节点时，再次尝试将资源分配到Worker节点
如果资源都分配给了Worker节点，查看当前节点是否存活，如果当前节点已经不存在了，就将该资源分配给其他的节点

分配资源的时机：
1. Master成为Leader
2. 客户端调用Master API进行资源的增删改查
3. Master监听到Worker节点发生变化
*/
func (m *Master) reAssign() {

	rs := make([]*ResourceSpec, 0, len(m.resources))

	m.rLock.Lock()
	defer m.rLock.Unlock()

	for _, r := range m.resources {
		if r.AssignedNode == "" {
			rs = append(rs, r)
			continue
		}

		id, err := getNodeId(r.AssignedNode)
		if err != nil {
			m.logger.Error("get nodeId failed", zap.Error(err))
		}

		if _, ok := m.workerNodes[id]; !ok {
			rs = append(rs, r)
		}
	}
	m.AddResources(rs)
}

func (m *Master) SetForwardClient(cli proto.CrawlerMasterService) {
	m.farwardCli = cli
}

func getNodeId(node string) (string, error) {

	nds := strings.Split(node, "|")
	if len(nds) < 2 {
		return "", errors.New("get nodeId error")
	}
	id := nds[0]
	return id, nil
}

func workerNodeDiff(old map[string]*NodeSpec, new map[string]*NodeSpec) ([]string, []string, []string) {

	added := make([]string, 0)
	deleted := make([]string, 0)
	changed := make([]string, 0)

	for k, v := range new {
		if ov, ok := old[k]; ok {
			if !reflect.DeepEqual(v, ov) {
				changed = append(changed, k)
			}
		} else {
			added = append(added, k)
		}
	}
	for k := range old {
		if _, ok := new[k]; ok {
			deleted = append(deleted, k)
		}
	}
	return added, deleted, changed
}

// 生成masterId
func getMasterId(id string, ipv4 string, address string) string {
	return "master" + id + "-" + ipv4 + address
}

// 获取本机网卡IP
func getLocalIP() (string, error) {
	var (
		addrs []net.Addr
		err   error
	)
	// 获取所有网卡
	if addrs, err = net.InterfaceAddrs(); err != nil {
		return "", err
	}
	// 取第一个非lo的网卡IP
	for _, addr := range addrs {
		if ipNet, isIpNet := addr.(*net.IPNet); isIpNet && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}

	return "", errors.New("no local ip")
}
