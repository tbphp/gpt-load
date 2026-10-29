package gateway

import (
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// WebRTC 只走回环网卡：真实网卡（Wi-Fi、VPN 等）会让每次关闭连接多等约 1 秒，结果也依赖开发机网络。
	liveMediaLoopbackOnly = true
	// 吊销用例靠授权复查发现变化，缩短周期以免每个用例空等 2 秒。
	liveAuthorizationPeriod = 20 * time.Millisecond
	m.Run()
}
