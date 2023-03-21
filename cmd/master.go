package cmd

import (
	"context"
	"github.com/bob2325168/spider/proto/crawler"
	"github.com/bob2325168/spider/spider"
	"github.com/juju/ratelimit"
	"net/http"
	"time"

	"github.com/bob2325168/spider/master"
	"github.com/bob2325168/spider/middlewares/logger"
	grpccli "github.com/go-micro/plugins/v4/client/grpc"
	"github.com/go-micro/plugins/v4/config/encoder/toml"
	"github.com/go-micro/plugins/v4/registry/etcd"
	gs "github.com/go-micro/plugins/v4/server/grpc"
	"github.com/go-micro/plugins/v4/wrapper/breaker/hystrix"
	ratePlugin "github.com/go-micro/plugins/v4/wrapper/ratelimiter/ratelimit"
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

func init() {
	MasterCmd.Flags().StringVar(&masterId, "id", "1", "set master id")
	MasterCmd.Flags().StringVar(&HTTPListenAddress, "http", ":8081", "set HTTP listen address")
	MasterCmd.Flags().StringVar(&GRPCListenAddress, "grpc", ":9091", "set GRPC listen address")
	MasterCmd.Flags().StringVar(&PProfListenAddress, "pprof", ":9981", "set pprof listen address")
}

var MasterCmd = &cobra.Command{
	Use:   "master",
	Short: "run master service",
	Long:  "run master service",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runMaster()
	},
}

func runMaster() {

	// start pprof
	go func() {
		if err := http.ListenAndServe(PProfListenAddress, nil); err != nil {
			panic(err)
		}
	}()

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

	seeds := parseTaskConfig(log, nil, nil, tcfg)

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
	go runMasterHTTPServer(sConfig)

	// 启动grpc服务器
	runMasterGRPCServer(m, log, reg, sConfig)
}

func runMasterGRPCServer(masterService *master.Master, log *zap.Logger, reg registry.Registry, cfg ServerConfig) {

	// 令牌桶算法限流：每秒放0.5个令牌
	b := ratelimit.NewBucketWithRate(0.5, 1)

	service := micro.NewService(
		micro.Server(gs.NewServer(server.Id(masterId))),
		micro.Address(GRPCListenAddress),
		micro.Registry(reg),
		micro.RegisterTTL(time.Duration(cfg.RegisterTTL)*time.Second),
		micro.RegisterInterval(time.Duration(cfg.RegisterInterval)*time.Second),
		micro.Name(cfg.Name),
		micro.WrapHandler(logger.LogWrapper(log)),
		micro.Client(grpccli.NewClient()),
		micro.WrapHandler(ratePlugin.NewHandlerWrapper(b, false)),
		micro.WrapClient(hystrix.NewClientWrapper()),
	)
	//zap.S().Debug("master id:", masterId)

	// 设置熔断器
	// 熔断的维度是：go.micro.server.master.CrawlerMaster.AddResource, 也就是服务名+方法名
	hystrix.ConfigureCommand("go.micro.server.master.CrawlerMaster.AddResource", hystrix.CommandConfig{
		Timeout:                10000, // 超时时间
		MaxConcurrentRequests:  100,   // 最大并发数
		ErrorPercentThreshold:  30,    // 错误百分比，超过这个值就触发熔断
		RequestVolumeThreshold: 10,    // 触发熔断器的最小请求数
		SleepWindow:            30,    // 熔断后多久尝试恢复
	})

	cli := crawler.NewCrawlerMasterService(cfg.Name, service.Client())
	masterService.SetForwardClient(cli)

	//设置micro客户端默认超时时间
	if err := service.Client().Init(client.RequestTimeout(time.Duration(cfg.ClientTimeOut) * time.Second)); err != nil {
		log.Sugar().Error("micro client init error", zap.String("error:", err.Error()))
		return
	}

	service.Init()

	if err := crawler.RegisterCrawlerMasterHandler(service.Server(), masterService); err != nil {
		log.Fatal("register handler failed", zap.Error(err))
	}

	//启动GRPC服务
	if err := service.Run(); err != nil {
		log.Fatal("master grpc server stop")
	}
}

func runMasterHTTPServer(cfg ServerConfig) {

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
