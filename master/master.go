package master

import (
	"github.com/pkg/errors"
	"go-micro.dev/v4/registry"
	"net"
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

}

func getMasterId(id string, ipv4 string, address string) string {
	return ""
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
