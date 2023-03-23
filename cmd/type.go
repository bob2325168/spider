package cmd

var ServiceName = "go.micro.server.worker"
var workerId string
var masterId string
var HTTPListenAddress string
var GRPCListenAddress string
var PProfListenAddress string
var cluster bool
var podIP string
var cfgFile string

type ServerConfig struct {
	RegistryAddress  string
	RegisterTTL      int
	RegisterInterval int
	Name             string
	ClientTimeOut    int
}
