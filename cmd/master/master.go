package master

import (
	"context"
	"github.com/bob2325168/spider/cmd/worker"
	"github.com/bob2325168/spider/proto/crawler"
	"github.com/bob2325168/spider/spider"
	"net/http"
	"time"

	"github.com/bob2325168/spider/master"
	"github.com/bob2325168/spider/middlewares/logger"
	grpccli "github.com/go-micro/plugins/v4/client/grpc"
	"github.com/go-micro/plugins/v4/config/encoder/toml"
	"github.com/go-micro/plugins/v4/registry/etcd"
	gs "github.com/go-micro/plugins/v4/server/grpc"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/spf13/cobra"
	"go-micro.dev/v4"
	"go-micro.dev/v4/client"
	"go-micro.dev/v4/config"
	"go-micro.dev/v4/config/reader"
	"go-micro.dev/v4/config/reader/json"
	"go-micro.dev/v4/config/source"
	"go-micro.dev/v4/config/source/file"
	"go-micro.dev/v4/registry"
	"go-micro.dev/v4/server"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var masterId string
var HTTPListenAddress string
var GRPCListenAddress string
var PProfListenAddress string

func init() {
	Cmd.Flags().StringVar(&masterId, "id", "1", "set master id")
	Cmd.Flags().StringVar(&HTTPListenAddress, "http", ":8081", "set HTTP listen address")
	Cmd.Flags().StringVar(&GRPCListenAddress, "grpc", ":9091", "set GRPC listen address")
	Cmd.Flags().StringVar(&PProfListenAddress, "pprof", ":9981", "set pprof listen address")
}

var Cmd = &cobra.Command{
	Use:   "master",
	Short: "run master service",
	Long:  "run master service",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		run()
	},
}

func run() {

	//// start pprof
	//go func() {
	//	if err := http.ListenAndServe(PProfListenAddress, nil); err != nil {
	//		panic(err)
	//	}
	//}()

	var (
		err      error
		log      *zap.Logger
		logLevel zapcore.Level
	)

	// 加载配置文件，如果加载失败直接panic
	enc := toml.NewEncoder()
	cfg, err := config.NewConfig(config.WithReader(json.NewReader(reader.WithEncoder(enc))))
	if err := cfg.Load(file.NewSource(
		file.WithPath("config.toml"),
		source.WithEncoder(enc),
	)); err != nil {
		panic(err)
	}

	// 设置标准的日志输入格式
	logText := cfg.Get("logLevel").String("INFO")
	if logLevel, err = zapcore.ParseLevel(logText); err != nil {
		panic(err)
	}
	plugin := logger.NewStdoutPlugin(logLevel)
	log = logger.NewLogger(plugin)
	log.Info("log init end")

	// 设置zap全局的logger
	zap.ReplaceGlobals(log)

	var sConfig ServerConfig
	if err := cfg.Get("MasterServer").Scan(&sConfig); err != nil {
		log.Error("get master grpc server config failed", zap.Error(err))
	}
	log.Sugar().Debugf("master grpc server config, %+v", sConfig)

	// 注册etcd的配置中心
	reg := etcd.NewRegistry(registry.Addrs(sConfig.RegistryAddress))

	// 初始化任务task
	var tcfg []spider.TaskConfig
	if err := cfg.Get("Tasks").Scan(&tcfg); err != nil {
		log.Error("init seed tasks", zap.Error(err))
	}

	seeds := worker.ParseTaskConfig(log, nil, nil, tcfg)

	m, err := master.New(
		masterId,
		master.WithLogger(log.Named("master")),
		master.WithGRPCAddress(GRPCListenAddress),
		master.WithregistryURL(sConfig.RegistryAddress),
		master.WithRegistry(reg),
		master.WithSeeds(seeds),
	)
	if err != nil {
		log.Error("init master failed", zap.Error(err))
	}

	// 启动http proxy to grpc
	go runHTTPServer(sConfig)

	// 启动grpc服务器
	runGRPCServer(m, log, reg, sConfig)
}

type ServerConfig struct {
	RegistryAddress  string
	RegisterTTL      int
	RegisterInterval int
	Name             string
	ClientTimeOut    int
}

func runGRPCServer(masterService *master.Master, logger *zap.Logger, reg registry.Registry, cfg ServerConfig) {

	service := micro.NewService(
		micro.Server(gs.NewServer(server.Id(masterId))),
		micro.Address(GRPCListenAddress),
		micro.Registry(reg),
		micro.RegisterTTL(time.Duration(cfg.RegisterTTL)*time.Second),
		micro.RegisterInterval(time.Duration(cfg.RegisterInterval)*time.Second),
		micro.Name(cfg.Name),
		micro.WrapHandler(logWrapper(logger)),
		micro.Client(grpccli.NewClient()),
	)

	cli := crawler.NewCrawlerMasterService(cfg.Name, service.Client())
	masterService.SetForwardClient(cli)

	//设置micro客户端默认超时时间
	if err := service.Client().Init(client.RequestTimeout(time.Duration(cfg.ClientTimeOut) * time.Second)); err != nil {
		logger.Sugar().Error("micro client init error", zap.String("error:", err.Error()))
		return
	}

	service.Init()

	if err := crawler.RegisterCrawlerMasterHandler(service.Server(), masterService); err != nil {
		logger.Fatal("register handler failed", zap.Error(err))
	}

	//启动GRPC服务
	if err := service.Run(); err != nil {
		logger.Fatal("master grpc server stop")
	}
}

func runHTTPServer(cfg ServerConfig) {

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	if err := crawler.RegisterCrawlerMasterGwFromEndpoint(ctx, mux, GRPCListenAddress, opts); err != nil {
		zap.L().Fatal("register backend grpc server endpoint failed")
	}
	zap.S().Debugf("start http server listening on %v proxy to grpc server;%v", HTTPListenAddress, GRPCListenAddress)

	// 启动http服务
	if err := http.ListenAndServe(HTTPListenAddress, mux); err != nil {
		zap.L().Fatal("http listenAndServe failed")
	}
}

// 使用go函数闭包的特性，对请求进行封装
// 中间件函数在接收到GRPC请求时，可以打印出请求的具体参数，方便排查问题
func logWrapper(log *zap.Logger) server.HandlerWrapper {
	return func(fn server.HandlerFunc) server.HandlerFunc {
		return func(ctx context.Context, req server.Request, rsp interface{}) error {
			log.Info("receive request",
				zap.String("method", req.Method()),
				zap.String("service", req.Service()),
				zap.Reflect("request param:", req.Body()),
			)
			err := fn(ctx, req, rsp)
			return err
		}
	}
}
