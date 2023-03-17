package master

import (
	"context"
	"github.com/bob2325168/spider/collect"
	sqlstorage2 "github.com/bob2325168/spider/db/storage/sqlstorage"
	"github.com/bob2325168/spider/engine"
	"github.com/bob2325168/spider/middlewares/limiter"
	"github.com/bob2325168/spider/middlewares/logger"
	gt "github.com/bob2325168/spider/proto/greeter"
	"github.com/bob2325168/spider/proxy"
	"github.com/bob2325168/spider/spider"
	"github.com/go-micro/plugins/v4/config/encoder/toml"
	etcdReg "github.com/go-micro/plugins/v4/registry/etcd"
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
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net/http"
	"time"
)

var masterId string
var HTTPListenAddress string
var GRPCListenAddress string

func init() {
	Cmd.Flags().StringVar(&masterId, "id", "1", "set master id")
	Cmd.Flags().StringVar(&HTTPListenAddress, "http", ":8081", "set HTTP listen address")
	Cmd.Flags().StringVar(&GRPCListenAddress, "grpc", ":9091", "set GRPC listen address")
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
	var (
		err      error
		log      *zap.Logger
		p        proxy.Func
		store    spider.Storage
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

	// 设置fetcher
	proxyURLs := cfg.Get("fetcher", "proxy").StringSlice([]string{})
	timeout := cfg.Get("fetcher", "timeout").Int(5000)
	log.Sugar().Info("proxy list: ", proxyURLs, " timeout: ", timeout)

	if p, err = proxy.RoundRobinProxySwitcher(proxyURLs...); err != nil {
		log.Error("roundRobinProxySwitcher failed")
		return
	}

	var f spider.Fetcher = &collect.BrowserFetch{
		Timeout: time.Duration(timeout) * time.Millisecond,
		Proxy:   p,
		Logger:  log,
	}

	// 设置存储
	sqlURL := cfg.Get("storage", "sqlURL").String("")
	if store, err = sqlstorage2.New(
		sqlstorage2.WithSqlURL(sqlURL),
		sqlstorage2.WithLogger(log.Named("sqlDB")),
		sqlstorage2.WithBatchCount(2),
	); err != nil {
		log.Error("create sqlstorage failed", zap.Error(err))
		return
	}

	// 初始化task
	var tcfg []spider.TaskConfig
	if err := cfg.Get("Tasks").Scan(&tcfg); err != nil {
		log.Error("init seed tasks", zap.Error(err))
	}
	seeds := parseTaskConfig(log, f, store, tcfg)

	s := engine.NewEngine(
		engine.WithFetcher(f),
		engine.WithLogger(log),
		engine.WithWorkCount(5),
		engine.WithSeeds(seeds),
		engine.WithScheduler(engine.NewSchedule()),
	)

	// 启动worker
	go s.Run()

	var sconfig ServerConfig
	if err := cfg.Get("MasterServer").Scan(&sconfig); err != nil {
		log.Error("get master grpc server config failed", zap.Error(err))
	}
	log.Sugar().Debugf("master grpc server config, %+v", sconfig)

	// 启动http proxy to grpc
	go runHTTPServer(sconfig)

	// 启动grpc服务器
	runGRPCServer(log, sconfig)
}

type ServerConfig struct {
	RegistryAddress  string
	RegisterTTL      int
	RegisterInterval int
	Name             string
	ClientTimeOut    int
}

func runGRPCServer(logger *zap.Logger, cfg ServerConfig) {

	reg := etcdReg.NewRegistry(registry.Addrs(cfg.RegistryAddress))
	service := micro.NewService(
		micro.Server(gs.NewServer(server.Id(masterId))),
		micro.Address(GRPCListenAddress),
		micro.Registry(reg),
		micro.RegisterTTL(time.Duration(cfg.RegisterTTL)*time.Second),
		micro.RegisterInterval(time.Duration(cfg.RegisterInterval)*time.Second),
		micro.Name(cfg.Name),
		micro.WrapHandler(logWrapper(logger)),
	)

	//设置micro客户端默认超时时间
	if err := service.Client().Init(client.RequestTimeout(time.Duration(cfg.ClientTimeOut) * time.Second)); err != nil {
		logger.Sugar().Error("micro client init error", zap.String("error:", err.Error()))
		return
	}
	service.Init()

	if err := gt.RegisterGreeterHandler(service.Server(), new(Greeter)); err != nil {
		logger.Fatal("register handler failed")
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

	if err := gt.RegisterGreeterGwFromEndpoint(ctx, mux, GRPCListenAddress, opts); err != nil {
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

// 从配置文件中读取值
func parseTaskConfig(log *zap.Logger, f spider.Fetcher,
	s spider.Storage, cfgs []spider.TaskConfig) []*spider.Task {

	tasks := make([]*spider.Task, 0, 1000)
	for _, cfg := range cfgs {
		t := spider.NewTask(
			spider.WithName(cfg.Name),
			spider.WithReload(cfg.Reload),
			spider.WithCookie(cfg.Cookie),
			spider.WithLogger(log),
			spider.WithStorage(s),
		)

		if cfg.WaitTime > 0 {
			t.WaitTime = cfg.WaitTime
		}
		if cfg.MaxDepth > 0 {
			t.MaxDepth = cfg.MaxDepth
		}

		var limits []limiter.RateLimiter
		if len(cfg.Limits) > 0 {
			for _, lcfg := range cfg.Limits {
				l := rate.NewLimiter(limiter.Per(lcfg.EventCount, time.Duration(lcfg.EventDur)*time.Second), 1)
				limits = append(limits, l)
			}
			multiLimitter := limiter.Multi(limits...)
			t.Limiter = multiLimitter
		}

		switch cfg.Fetcher {
		case "browser":
			t.Fetcher = f
		}
		tasks = append(tasks, t)
	}
	return tasks
}

// Greeter 实现 GRPC greeter interface
type Greeter struct {
}

func (g Greeter) Hello(ctx context.Context, request *gt.Request, response *gt.Response) error {
	response.Greeting = "Hello" + request.Name
	return nil
}
