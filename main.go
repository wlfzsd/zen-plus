package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/app"
	"github.com/irbis-sh/zen-desktop/internal/autostart"
	"github.com/irbis-sh/zen-desktop/internal/config"
	"github.com/irbis-sh/zen-desktop/internal/constants"
	"github.com/irbis-sh/zen-desktop/internal/logger"
	"github.com/irbis-sh/zen-desktop/internal/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

const (
	windowWidth  = 450
	windowHeight = 670
)

//go:embed all:frontend/dist
var assets embed.FS

// Memory bounds (2026-10-03, retuned 2026-10-04 twice): stock Zen runs on
// Go's default GOGC=100, which lets the heap grow to twice the live set
// before collecting and hands freed memory back to the OS only lazily. As a
// system-wide proxy serving every application's traffic around the clock,
// allocation bursts from request filtering ratchet the resident set up to
// multi-GiB peaks that never come back down (upstream issue #388 proposed
// SetGCPercent(5), never merged). These bounds keep the heap target close to
// the live set while leaving headroom so the soft limit never becomes what
// drives collection:
//   - GCPercent 40: collect after each ~40% of heap growth (target = live
//     set × 1.4). A GODEBUG=gctrace experiment (2026-10-04) measured GC CPU
//     at 7% of machine capacity under synthetic browser-like churn at
//     GCPercent 10 versus 2% at 40 - the tighter value burned multiple cores
//     under real browsing (user-observed 20-40% CPU) for ~80 MiB of envelope,
//     so it was rolled back. The GC is concurrent and the workload is
//     I/O-bound; memory stays bounded by the limit below.
//   - MemoryLimit 1 GiB: a soft ceiling far above the actual live set
//     (rule trees, certificate cache, connection pools) that only engages
//     during extreme bursts, bounding the worst case.
//   - FreeOSMemory every 10 min: idle periods return memory to the OS within
//     minutes instead of half-hour strides.
func configureMemoryBounds() {
	const (
		gcPercent             = 40
		memoryLimitMiB        = 1024
		memoryReleaseInterval = 10 * time.Minute
	)

	debug.SetGCPercent(gcPercent)
	debug.SetMemoryLimit(memoryLimitMiB << 20)
	go releaseMemoryPeriodically(memoryReleaseInterval)
	log.Printf("memory bounds set: GCPercent=%d, soft memory limit=%d MiB, periodic release every %v", gcPercent, memoryLimitMiB, memoryReleaseInterval)
}

// releaseMemoryPeriodically returns idle heap memory to the OS on a fixed
// schedule: debug.FreeOSMemory forces a full collection followed by a
// scavenge, so long idle stretches (nights, unattended machines) do not keep
// the last burst's peak resident. Same deliberate never-exiting shape as the
// certificate cache cleanup goroutine.
func releaseMemoryPeriodically(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		debug.FreeOSMemory()
	}
}

func main() {
	startOnDomReady := flag.Bool("start", false, "Start the service when DOM is ready")
	startHidden := flag.Bool("hidden", false, "Start the application in hidden mode")
	uninstallCA := flag.Bool("uninstall-ca", false, "Uninstall the CA and exit")
	flag.Parse()

	// 2026-10-08 诊断插桩（临时）：ZEN_PPROF=1 时在 127.0.0.1:6399 暴露 pprof，
	// 用于定位 CPU/内存回归；默认完全关闭，生产行为不变。
	if os.Getenv("ZEN_PPROF") == "1" {
		go func() {
			log.Println("diagnostic pprof listening on 127.0.0.1:6399")
			if err := http.ListenAndServe("127.0.0.1:6399", nil); err != nil {
				log.Printf("diagnostic pprof exited: %v", err)
			}
		}()
	}

	err := logger.SetupLogger()
	if err != nil {
		log.Printf("failed to setup logger: %v", err)
	}
	// After SetupLogger so the bounds land in application.log where they can
	// be verified at runtime (2026-10-03).
	configureMemoryBounds()
	log.Printf("initializing the app; version=%q", config.Version)

	appConfig, err := config.New()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	app, err := app.NewApp(constants.AppName, appConfig, *startOnDomReady)
	if err != nil {
		log.Fatalf("failed to create app: %v", err)
	}

	if *uninstallCA {
		if err := app.UninstallCA(); err != nil {
			// UninstallCA logs the error internally
			os.Exit(1)
		}

		log.Println("CA uninstalled successfully")
		return
	}

	autostart := &autostart.Manager{}

	err = wails.Run(&options.App{
		Title:         constants.AppName,
		Width:         windowWidth,
		Height:        windowHeight,
		DisableResize: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:     app.Startup,
		OnBeforeClose: app.BeforeClose,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               constants.InstanceID,
			OnSecondInstanceLaunch: app.OnSecondInstanceLaunch,
		},
		Bind: []interface{}{
			app,
			appConfig,
			autostart,
		},
		EnumBind: []interface{}{
			config.UpdatePolicyEnum,
			config.RoutingModeEnum,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   constants.AppName,
				Message: fmt.Sprintf("Your Comprehensive Ad-Blocker and Privacy Guard\nVersion: %s\n© 2026 Zen contributors", config.Version),
			},
		},
		HideWindowOnClose: runtime.GOOS == "darwin" || runtime.GOOS == "windows" || (runtime.GOOS == "linux" && systray.Available()),
		StartHidden:       *startHidden,
	})

	if err != nil {
		log.Fatal(err)
	}

	app.RunPendingRestart()
}
