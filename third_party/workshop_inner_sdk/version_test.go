package clt01

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b FirmwareVersion
		want int
	}{
		{"1.5.2", "1.5.2", 0},
		{"1.5.3", "1.5.2", 1},
		{"1.5.2", "1.5.3", -1},
		{"v1.5.3", "1.5.3", 0}, // v 前缀归一
		{"1.6", "1.5.9", 1},    // 缺省段按 0 → 1.6.0
		{"1.5.10", "1.5.9", 1}, // 数值比较而非字典序
		{"1.5", "1.5.0.0", 0},  // 尾部全 0 段等价
		{"1.5.4", "1.5.3", 1},  // 未来补丁版
		{"1.6.0", "1.5.9", 1},  // 次版本跨越
		{"2.0", "1.9.9", 1},    // 主版本
		{"1.4.2", "1.5.0", -1}, // legacy 层
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
		// 对称性
		if got2 := CompareVersions(c.b, c.a); got2 != -c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d (symmetry)", c.b, c.a, got2, -c.want)
		}
	}
}

func TestAtLeastThresholds(t *testing.T) {
	// 未来版本无需改 SDK：阈值比较自动落入正确能力层
	if !(FirmwareVersion("v1.5.4").AtLeast(VerChunkedUpload)) {
		t.Error("1.5.4 应具备 chunked 能力层")
	}
	if !(FirmwareVersion("1.6.0").AtLeast(VerChunkedUpload)) {
		t.Error("1.6.0 应具备 chunked 能力层")
	}
	if FirmwareVersion("1.5.2").AtLeast(VerChunkedUpload) {
		t.Error("1.5.2 不应具备 chunked 能力层")
	}
	if !FirmwareVersion("1.5.2").AtLeast(VerAsyncUpload) {
		t.Error("1.5.2 应具备 async 能力层")
	}
	if FirmwareVersion("1.4.9").AtLeast(VerAsyncUpload) {
		t.Error("1.4.9 不应具备 async 能力层")
	}
}

func TestUploadVRMOptVersionDispatch(t *testing.T) {
	// implFor 按阈值分发：未来版本号自动选择正确实现
	c := &Client{fw: &firmwareState{}}
	if _, ok := implFor("1.6.0", c).(*v153Impl); !ok {
		t.Error("1.6.0 应分发到 chunked 实现")
	}
	if _, ok := implFor("1.5.4", c).(*v153Impl); !ok {
		t.Error("1.5.4 应分发到 chunked 实现")
	}
	if _, ok := implFor("1.5.2", c).(*v152Impl); !ok {
		t.Error("1.5.2 应分发到 async 实现")
	}
	if _, ok := implFor("1.4.0", c).(*legacyImpl); !ok {
		t.Error("1.4.0 应分发到 legacy 实现")
	}
}
