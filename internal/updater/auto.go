package updater

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// startupDelay 启动后首次检查的延迟(避开启动高峰,减少对首屏的影响)。
	startupDelay = 30 * time.Second
	// checkInterval 之后每轮检查间隔。
	checkInterval = 6 * time.Hour
	// applyingDelay 提示前端后等待再启动安装器(给 Toast 留显示时间)。
	applyingDelay = 2 * time.Second
)

// autoMu 防止多轮检查并发触发升级流程。
var autoMu sync.Mutex

// StartAuto 自动升级循环:ManifestURL 为空或版本为 dev 时直接返回(不检查)。
// 流程:启动延迟 30s 后首次检查 → 之后每 6 小时一次 → 发现新版本则
// 下载校验 → onApplying(version) 通知前端 → 静默安装 → 退出本进程,
// 由安装器完成后重启应用。所有错误仅记日志,绝不影响应用正常运行。
func StartAuto(ctx context.Context, current string, onApplying func(version string)) {
	if ManifestURL == "" || current == "dev" {
		log.Printf("[updater] 未配置更新清单或为 dev 构建,跳过更新检查")
		return
	}

	time.Sleep(startupDelay)
	runOnce := func() {
		autoMu.Lock()
		defer autoMu.Unlock()
		if err := tryUpdate(ctx, current, onApplying); err != nil {
			log.Printf("[updater] %v", err)
		}
	}

	runOnce()
	t := time.NewTicker(checkInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runOnce()
		}
	}
}

// tryUpdate 执行一次"检查→下载→安装→退出"流程。
// 到达安装步骤时本进程退出,不再返回。
func tryUpdate(ctx context.Context, current string, onApplying func(version string)) error {
	entry, version, ok, err := CheckLatest(ctx, current)
	if err != nil {
		return err // 网络/清单问题,静默等下轮
	}
	if !ok {
		return nil
	}
	log.Printf("[updater] 发现新版本 %s(当前 %s),开始下载", version, current)

	path, err := DownloadInstaller(ctx, entry, version)
	if err != nil {
		return err
	}
	log.Printf("[updater] 安装包校验通过: %s", path)

	if onApplying != nil {
		onApplying(version)
	}
	time.Sleep(applyingDelay)

	if err := Apply(installerCmd(path)); err != nil {
		return err
	}
	log.Printf("[updater] 安装器已启动,退出应用等待升级")
	quitForUpdate(ctx)
	return nil
}

// quitForUpdate 触发应用优雅退出(经 Wails 触发 OnShutdown 清理托盘),
// 3 秒兜底强杀,防止托盘清理卡住导致升级流程停滞。
func quitForUpdate(ctx context.Context) {
	quitOnce.Do(func() {
		go func() {
			time.Sleep(3 * time.Second)
			os.Exit(0)
		}()
		wailsruntime.Quit(ctx)
	})
}

// quitOnce 保证兜底定时器只挂一次。
var quitOnce sync.Once

// Apply 启动安装器(静默参数由 installerCmd 构造)。返回后调用方应退出应用。
// 静默安装完成后安装器会自动重启应用(见 project.nsi 的 IfSilent Exec)。
func Apply(cmd interface{ Start() error }) error {
	return cmd.Start()
}
