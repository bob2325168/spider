package master

import (
	"context"
	"github.com/bob2325168/spider/cmd/worker"
	"github.com/go-acme/lego/v4/log"
	"github.com/pkg/errors"
	"go-micro.dev/v4/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.uber.org/zap"
	"net"
	"reflect"
	"sync/atomic"
	"time"
)

type Master struct {
	Id          string
	ready       int32
	leaderId    string
	workerNodes map[string]*registry.Node
	options
}

func New(id string, opts ...Option) (*Master, error) {

	m := &Master{}
	options := defaultOptions
	for _, opt := range opts {
		opt(&options)
	}
	m.options = options
	ipv4, err := getLocalIP()
	if err != nil {
		return nil, err
	}
	m.Id = getMasterId(id, ipv4, m.GRPCAddress)
	m.logger.Sugar().Debugln("master-id", m.Id)
	go m.campaign()

	return m, nil
}

func (m *Master) campaign() {

	endpoints := []string{m.registryURL}
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		panic(err)
	}

	sess, err := concurrency.NewSession(cli, concurrency.WithTTL(5))
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
					m.selectedForLeader()
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
			m.updateNodes()
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

// 是否为leader
func (m *Master) isLeader() bool {
	return atomic.LoadInt32(&m.ready) != 0
}

func (m *Master) elect(e *concurrency.Election, ch chan error) {
	// 一直阻塞，直到选举成功
	err := e.Campaign(context.Background(), m.Id)
	ch <- err
}

func (m *Master) watchWorker() chan *registry.Result {

	watch, err := m.registry.Watch(registry.WatchService(worker.ServiceName))
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

func (m *Master) selectedForLeader() {
	atomic.StoreInt32(&m.ready, 1)
}

func (m *Master) updateNodes() {
	services, err := m.registry.GetService(worker.ServiceName)
	if err != nil {
		m.logger.Error("get service", zap.Error(err))
	}

	nodes := make(map[string]*registry.Node)
	if len(services) > 0 {
		for _, spec := range services[0].Nodes {
			nodes[spec.Id] = spec
		}
	}

	added, deleted, changed := workerNodeDiff(m.workerNodes, nodes)
	m.logger.Sugar().Info("worker joined: ", added, ", deleted: ", deleted, ", changed: ", changed)

	m.workerNodes = nodes
}

func workerNodeDiff(old map[string]*registry.Node, new map[string]*registry.Node) ([]string, []string, []string) {

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
