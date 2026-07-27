package web

import (
	"strings"
	"testing"
)

func TestPageTunnelManagementContracts(t *testing.T) {
	required := []string{
		`id="tunnels"`,
		`<h2 id="tunnels-title">活动隧道</h2>`,
		`<th>本地映射</th>`,
		`<th>运行时长</th>`,
		`<th>重连</th>`,
		`waiting_reconnect: '等待重连'`,
		`reconnecting: '正在重连'`,
		`formatDuration(item.runningSince)`,
		`retryText(item.nextRetryAt)`,
		`/logs'`,
		`expandedLogs`,
		`entry.diagnostic`,
		`actionButton('代理'`,
		`actionButton('重新代理'`,
		`actionButton('停止'`,
		`actionButton('清除'`,
		`navigator.clipboard.writeText(address)`,
		`window.open('http://' + address + '/'`,
		`method: 'POST'`,
		`method: 'DELETE'`,
		`id="shutdown"`,
		`fetch('/api/shutdown', { method: 'POST' })`,
		`退出程序将停止所有 SSH 隧道`,
		`new Map(tunnelItems.map(item => [tunnelKey(item.host, item.remotePort), item]))`,
	}
	for _, fragment := range required {
		if !strings.Contains(pageHTML, fragment) {
			t.Errorf("页面缺少隧道管理契约 %q", fragment)
		}
	}

	if count := strings.Count(pageHTML, `fetch('/api/tunnels')`); count != 1 {
		t.Fatalf("每轮加载应只声明一次隧道列表请求，实际为 %d", count)
	}
	for _, forbidden := range []string{"beforeunload", "pagehide", "sendBeacon"} {
		if strings.Contains(pageHTML, forbidden) {
			t.Errorf("页面不得通过 %s 绑定隧道生命周期", forbidden)
		}
	}
}

func TestPageManagedHostAndChallengeContracts(t *testing.T) {
	required := []string{
		`id="add-host"`,
		`<th>目标地址</th>`,
		`<th>跳板</th>`,
		`id="host-dialog"`,
		`id="host-alias"`,
		`id="host-jump"`,
		`id="host-key-dialog"`,
		`/api/ssh-hosts`,
		`method = hostDialog.dataset.alias ? 'PUT' : 'POST'`,
		`method: 'DELETE'`,
		`candidate.jumpHost`,
		`host.valid === false`,
		`credential_required`,
		`host_key_confirmation_required`,
		`stageHost`,
		`confirmFingerprint`,
		`指纹变化会被拒绝`,
		`删除 ' + host.alias + ' 后会同时清理`,
	}
	for _, fragment := range required {
		if !strings.Contains(pageHTML, fragment) {
			t.Errorf("页面缺少 Host 管理或连接挑战契约 %q", fragment)
		}
	}
}
