package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/QuantumNous/new-api/relay"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		os.Exit(jsplugin.RunCLI(os.Args[2:], os.Stdout, os.Stderr))
	}
	startTime := time.Now()
	kitutil.SetLogging(common.SysLog, func(message string) {
		logger.LogError(nil, message)
	})
	kitutil.SetSystemErrorLogging(common.SysError)

	err := InitResources()
	if err != nil {
		common.FatalLog(common.LogText("failed to initialize resources: %s", err.Error()))
		return
	}

	common.SysLog(common.LogText("New API %s started", common.Version))
	metricsServer := appmetrics.StartServer()
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog(common.LogText("running in debug mode"))
	}

	kitutil.Debug.Store(common.DebugEnabled)

	defer func() {
		err := model.CloseDB()
		if err != nil {
			common.FatalLog(common.LogText("failed to close database: %s", err.Error()))
		}
	}()

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog(common.LogText("memory cache enabled"))
		common.SysLog(common.LogText("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(common.LogText("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, _, fixErr := model.FixAbility()
					if fixErr != nil {
						common.FatalLog(common.LogText("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			model.InitChannelCache()
		}()

		go model.SyncChannelCache(common.SyncFrequency)
	}
	wsmanager.StartSubscriber(context.Background())

	// Warm pricing after channel cache initialization so Advanced Custom
	// endpoint inference can read cached route settings on first request.
	model.GetPricing()

	// 热更新配置
	go model.SyncOptions(common.SyncFrequency)
	go controller.SyncTaskPlugins()

	// 周期性重载授权策略，保证多节点/多 master 部署下权限变更能传播到每个实例
	go authz.StartPolicySync(common.SyncFrequency)

	// 数据看板：统计增量保存在进程内存里，只有跑 relay 的进程有数据
	if common.RunsRelay() {
		go model.UpdateQuotaData()
	}

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" && common.RunsAdmin() {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			common.FatalLog(common.LogText("failed to parse CHANNEL_UPDATE_FREQUENCY: %s", err.Error()))
		}
		go controller.AutomaticallyUpdateChannels(frequency)
	}

	// Command Code reports its balance through an undocumented upstream route, so
	// the figure the metrics endpoint publishes is refreshed here rather than
	// derived from usage logs.
	if common.RunsAdmin() {
		go controller.StartCommandCodeBalanceRefresh(controller.CommandCodeBalanceRefreshInterval)
	}

	// Antigravity reports its quota as a remaining fraction per window instead of
	// a currency amount, so it is refreshed on its own loop.
	if common.RunsAdmin() {
		go controller.StartAntigravityQuotaRefresh(controller.AntigravityQuotaRefreshInterval)
	}

	// Mistral reports its monthly allowances to the key or the console session an
	// account entry carries, so the refresh is started by the entries the loader
	// accepts rather than by one credential's environment variable. The context is
	// cancelled at shutdown so the loop stops with the listener.
	mistralQuotaCtx, cancelMistralQuotaCtx := context.WithCancel(context.Background())
	defer cancelMistralQuotaCtx()
	if common.RunsAdmin() {
		if started, stopMistralQuotaRefresh := startMistralQuotaRefreshIfConfigured(mistralQuotaCtx); started {
			// Runs before the context is cancelled because it was deferred later:
			// it cancels its own child context and waits for the loop to return.
			defer stopMistralQuotaRefresh()
		}
	}

	// Codex credential auto-refresh check every 10 minutes, refresh when expires within 1 day
	if common.RunsAdmin() {
		service.StartCodexCredentialAutoRefreshTask()
		service.StartAntigravityCredentialAutoRefreshTask()
	}

	// Region quota leases: the admin tops leases up and folds reported spend
	// back into the central balance, a relay instance reports what it spent.
	service.StartRegionLeaseMaintenance()

	// Subscription quota reset task (daily/weekly/monthly/custom)
	if common.RunsAdmin() {
		service.StartSubscriptionQuotaResetTask()
	}

	stopSystemInstanceReporter := service.StartSystemInstanceReporter()

	// Wire task polling adaptor factory (breaks service -> relay import cycle).
	// Must run before the system task runner starts: the async_task_poll handler
	// calls service.RunTaskPollingOnce, which needs this factory set.
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Register the periodic channel test, upstream model update, and async task
	// polling (Midjourney / Suno / video) jobs as scheduled system tasks
	// (DB-lease dedup across masters + run history), then start the runner that
	// schedules and executes them. Master-only execution and the UpdateTask
	// switch are enforced inside the runner and each handler's Enabled().
	controller.RegisterScheduledSystemTasks()
	service.StartSystemTaskRunner()

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog(common.LogText("batch update enabled with interval %ss", strconv.Itoa(common.BatchUpdateInterval)))
		model.InitBatchUpdater()
	}
	if model.InitializeQuotaRefundRecovery() && common.RunsAdmin() {
		refundCtx, cancelRefundRecovery := context.WithCancel(context.Background())
		defer cancelRefundRecovery()
		go service.RunQuotaRefundRecovery(refundCtx)
	}

	host := os.Getenv("HOST")
	if os.Getenv("ENABLE_PPROF") == "true" {
		gopool.Go(func() {
			log.Println(http.ListenAndServe(net.JoinHostPort(host, "8005"), nil))
		})
		go common.Monitor()
		common.SysLog(common.LogText("pprof enabled"))
	}

	err = common.StartPyroScope()
	if err != nil {
		common.SysError(common.LogText("start pyroscope error : %v", err))
	}

	// Initialize HTTP server
	server := gin.New()
	if err := middleware.ConfigureTrustedProxies(server); err != nil {
		common.FatalLog(common.LogText("failed to configure trusted proxies: %s", err.Error()))
		return
	}
	server.Use(middleware.PanicRecover())
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.Version())
	server.Use(middleware.I18n())
	draining := middleware.NewDraining()
	server.GET("/healthz", draining.Readiness)
	server.Use(draining.RejectNewRequests())
	middleware.SetUpLogger(server)
	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// 设置路由
	router.SetRouter(server, router.WebAssets{
		BuildFS:   buildFS,
		IndexPage: indexPage,
	})
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	srv := &http.Server{
		Addr:    net.JoinHostPort(host, port),
		Handler: server,
	}

	stopRedactionProbe := controller.StartRedactionCapabilityProbe()
	defer stopRedactionProbe()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			common.FatalLog(common.LogText("failed to start HTTP server: %s", err.Error()))
		}
	}()

	time.Sleep(100 * time.Millisecond)

	common.LogStartupSuccess(startTime, host, port)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	common.SysLog(common.LogText("received signal: %v, shutting down...", sig))

	// The readiness probe fails from here on, so the pod leaves the Service
	// endpoints before the listener closes and no new request is routed to it.
	draining.Begin()
	stopRedactionProbe()
	time.Sleep(time.Duration(common.GetEnvOrDefault("SHUTDOWN_DRAIN_DELAY_SECONDS", 10)) * time.Second)

	// SSE streams may run for minutes; give them time to finish before forced exit
	shutdownTimeout := time.Duration(common.GetEnvOrDefault("SHUTDOWN_TIMEOUT_SECONDS", 120)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		common.SysError(common.LogText("server forced to shutdown: %v", err))
	}
	// A refund is two database statements, so this only bounds a stuck goroutine, not
	// normal work. It stays inside the deployment's 240 s drain budget.
	shutdownRefundWait := time.Duration(common.GetEnvOrDefault("SHUTDOWN_REFUND_WAIT_SECONDS", 10)) * time.Second
	// Refunds credit the wallet from a goroutine that the failing request returns
	// from, so they must be waited for before the flush. A flush that ran first would
	// write the debit and leave the matching credit unflushed, charging the user for
	// a request that failed. The wait is bounded because a hijacked WebSocket session
	// settles its quota at session end, which is not a shutdown concern; on timeout
	// the wait is logged and the flush still runs.
	if pending, finished := model.WaitDetachedQuotaWork(shutdownRefundWait); !finished {
		common.SysError(common.LogText("shutdown stopped waiting after %s with %d refund goroutines still running; flushing what is queued", shutdownRefundWait, pending))
	}
	// The listener is closed, so no new request queues a delta. On the routes the
	// server awaits, every handler has returned too, so what the flush writes is
	// every delta queued before it on those routes, refunds included. Two cases sit
	// outside that and can queue a delta after the flush: Shutdown timed out, and a
	// hijacked WebSocket relay (RelayFormatOpenAIRealtime, and the responses
	// WebSocket route), which net/http neither closes nor waits for and which settles
	// its quota at session end. A delta queued after the flush is lost, as it was
	// before this flush existed; because refunds are now awaited, an ordinary failed
	// request can no longer leave its debit written and its credit dropped.
	if common.BatchUpdateEnabled {
		model.FlushBatchUpdates()
	}
	stopSystemInstanceReporter()
	metricsCtx, metricsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer metricsCancel()
	appmetrics.Shutdown(metricsCtx, metricsServer)
	// 内存中的看板数据保存入库，避免重启丢失未落库数据 (issue #5679)
	if common.DataExportEnabled {
		model.SaveQuotaDataCache()
	}
	common.SysLog(common.LogText("server exited"))
}

// startMistralQuotaRefreshIfConfigured starts the background allowance refresh and
// reports whether it did, together with the function that stops it. The entries
// decide, not the environment variable that feeds one of them: an account reads its
// allowance with a key or with a console session, so an entry that carries a key
// alone is as configured as one that carries a cookie. A configuration that does not
// load is reported and leaves the refresh off, because an unreadable file is a
// mistake to surface rather than a reason to poll.
//
// The returned stop cancels the loop and waits for it to exit, so a caller that is
// shutting down does not return while a tick can still read the environment or write
// a channel balance. It is safe to call more than once.
func startMistralQuotaRefreshIfConfigured(ctx context.Context) (bool, func()) {
	accounts, err := service.LoadMistralQuotaAccounts()
	if err != nil {
		common.SysError(common.LogText(
			"failed to load the Mistral quota accounts, so their allowance is not refreshed: %s", err.Error()))
		return false, func() {}
	}
	if len(accounts) == 0 {
		return false, func() {}
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.StartMistralQuotaRefresh(loopCtx, mistralQuotaRefreshFrequency())
	}()
	return true, func() {
		cancel()
		<-done
	}
}

// mistralQuotaRefreshFrequency reads the configured refresh period. The variable
// is optional; a value that is not a positive number stops the process, because
// a typo would otherwise silently poll at the default period instead.
func mistralQuotaRefreshFrequency() time.Duration {
	frequency := controller.MistralQuotaRefreshInterval
	raw := os.Getenv("MISTRAL_VIBE_QUOTA_FREQUENCY")
	if raw == "" {
		return frequency
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil || minutes <= 0 {
		common.FatalLog(common.LogText("failed to parse MISTRAL_VIBE_QUOTA_FREQUENCY: %s", raw))
	}
	return time.Duration(minutes) * time.Minute
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--umami-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--Google Analytics-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
}

func InitResources() error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog(common.LogText("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables."))
		}
	}

	// 加载环境变量
	common.InitEnv()

	logger.SetupLogger()

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	// Initialize SQL Database
	err = model.InitDB()
	if err != nil {
		common.FatalLog(common.LogText("failed to initialize database: %s", err.Error()))
		return err
	}
	if err = authz.Init(model.DB); err != nil {
		common.FatalLog(common.LogText("failed to initialize authorization: %s", err.Error()))
		return err
	}
	if common.PasswordLoginEncryptionEnabled {
		if err = model.InitPasswordEncryption(); err != nil {
			common.FatalLog(common.LogText("failed to initialize password encryption: %s", err.Error()))
			return err
		}
	}

	model.CheckSetup()

	// Initialize options, should after model.InitDB()
	if common.IsMasterNode {
		if err := model.MigrateRetiredFrontendOptions(); err != nil {
			common.SysError(common.LogText("failed to migrate retired frontend options: %s", err.Error()))
		}
	}
	model.InitOptionMap()

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// Initialize SQL Database
	err = model.InitLogDB()
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	if common.RunsRelay() {
		perfmetrics.Init()
	}

	// 启动系统监控
	common.StartSystemMonitor()

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError(common.LogText("failed to initialize i18n: %s", err.Error()))
		// Don't return error, i18n is not critical
	} else {
		common.SysLog(common.LogText("i18n initialized with languages: %s", strings.Join(i18n.SupportedLanguages(), ", ")))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError(common.LogText("failed to load custom OAuth providers: %s", err.Error()))
		// Don't return error, custom OAuth is not critical
	}

	service.StartAuthArtifactCleanup()

	return nil
}
