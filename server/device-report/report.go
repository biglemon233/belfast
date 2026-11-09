// 提供白名单报告校验，拒绝额外属性并过滤可疑标识符。
//
// Provides allowlisted report validation, rejecting extra properties and suspicious identifiers.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Report 仅含机型类别信息，不含设备唯一标识符。
//
// Report contains model-level information, never unique device identifiers.
type Report struct {
	Backend          string   `json:"backend"`
	RunMode          string   `json:"runMode"`
	SchemaVersion    int      `json:"schemaVersion"`
	Manufacturer     string   `json:"manufacturer"`
	Brand            string   `json:"brand"`
	Model            string   `json:"model"`
	MarketingName    string   `json:"marketingName"`
	RomName          string   `json:"romName"`
	RomVersion       string   `json:"romVersion"`
	BuildDisplay     string   `json:"buildDisplay"`
	BuildIncremental string   `json:"buildIncremental"`
	Device           string   `json:"device"`
	Product          string   `json:"product"`
	Hardware         string   `json:"hardware"`
	SocModel         string   `json:"socModel"`
	SocManufacturer  string   `json:"socManufacturer"`
	AndroidRelease   string   `json:"androidRelease"`
	SdkInt           int      `json:"sdkInt"`
	SecurityPatch    string   `json:"securityPatch"`
	SupportedAbis    []string `json:"supportedAbis"`
	ScreenWidth      int      `json:"screenWidth"`
	ScreenHeight     int      `json:"screenHeight"`
	DensityDpi       int      `json:"densityDpi"`
	RefreshRate      int      `json:"refreshRate"`
	MemoryGiB        int      `json:"memoryGiB"`
	AppVersion       string   `json:"appVersion"`
	AppVersionCode   int      `json:"appVersionCode"`
	Compatibility    string   `json:"compatibility"`
}

var sensitiveValue = regexp.MustCompile(`(?i)(imei|imsi|serial|android[_ -]?id|mac[_ -]?address|\bSN\s*[:=]|[0-9]{14,}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|(?:[0-9a-f]{2}:){5}[0-9a-f]{2}|[\w.+-]+@[\w.-]+\.[a-z]{2,})`)
var safeDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func cleanText(value string) string {
	if sensitiveValue.MatchString(value) {
		return "[redacted]"
	}
	return strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return -1
		}
		if c == '@' {
			return '＠'
		}
		return c
	}, value))
}

func (r *Report) validate() error {
	if (r.Backend != "SHIZUKU" && r.Backend != "ROOT") ||
		(r.RunMode != "FOREGROUND" && r.RunMode != "BACKGROUND") {
		return errors.New("invalid mode")
	}
	if r.SchemaVersion != 2 || r.SdkInt < 28 || r.SdkInt > 100 ||
		r.ScreenWidth < 1 || r.ScreenWidth > 16384 ||
		r.ScreenHeight < 1 || r.ScreenHeight > 16384 ||
		r.DensityDpi < 1 || r.DensityDpi > 2000 ||
		r.RefreshRate < 1 || r.RefreshRate > 1000 ||
		r.MemoryGiB < 1 || r.MemoryGiB > 1024 || r.AppVersionCode < 1 {
		return errors.New("invalid numeric field")
	}
	if r.Compatibility != "working" &&
		r.Compatibility != "not_working" {
		return errors.New("invalid compatibility")
	}
	for _, value := range []*string{
		&r.Manufacturer, &r.Brand, &r.Model, &r.Device, &r.Product, &r.Hardware,
		&r.SocModel, &r.SocManufacturer, &r.AndroidRelease, &r.AppVersion,
		&r.MarketingName, &r.RomName, &r.RomVersion, &r.BuildDisplay, &r.BuildIncremental,
	} {
		if len(*value) > 256 {
			return errors.New("text field too long")
		}
		*value = cleanText(*value)
	}
	if r.Manufacturer == "" || r.Model == "" || r.AppVersion == "" || r.MarketingName == "" || r.RomName == "" {
		return errors.New("missing required field")
	}
	if r.SecurityPatch != "" && !safeDate.MatchString(r.SecurityPatch) {
		return errors.New("invalid security patch")
	}
	if len(r.SupportedAbis) < 1 || len(r.SupportedAbis) > 8 {
		return errors.New("invalid ABIs")
	}
	for _, abi := range r.SupportedAbis {
		switch abi {
		case "arm64-v8a", "armeabi-v7a", "armeabi", "x86", "x86_64", "riscv64":
		default:
			return errors.New("invalid ABI")
		}
	}
	return nil
}

func (r Report) digest() string {
	// 同一机型、系统和使用结果共享评论，App 版本升级不重复提交。
	r.AppVersion = ""
	r.AppVersionCode = 0
	body, _ := json.Marshal(r)
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func (r Report) issueBody(id string) string {
	escape := func(s string) string {
		return strings.ReplaceAll(html.EscapeString(s), "|", "&#124;")
	}
	status := map[string]string{
		"working":     "✅ 可正常使用 / Working",
		"not_working": "❌ 无法正常使用 / Not working",
	}[r.Compatibility]
	rows := [][2]string{
		{"机型 / Device name", r.MarketingName},
		{"厂商系统 / Vendor OS", strings.TrimSpace(r.RomName + " " + r.RomVersion)},
		{"完整系统版本 / Full system version", r.BuildIncremental},
		{"构建显示编号 / Build display", r.BuildDisplay},
		{"提权后端 / Privilege backend", r.Backend},
		{"运行模式 / Run mode", r.RunMode},
		{"厂商 / Manufacturer", r.Manufacturer}, {"品牌 / Brand", r.Brand},
		{"型号代码 / Model code", r.Model}, {"设备 / Device", r.Device},
		{"产品 / Product", r.Product}, {"硬件 / Hardware", r.Hardware},
		{"SoC", r.SocManufacturer + " " + r.SocModel},
		{"Android", fmt.Sprintf("%s (API %d)", r.AndroidRelease, r.SdkInt)},
		{"安全补丁 / Security patch", r.SecurityPatch},
		{"ABI", strings.Join(r.SupportedAbis, ", ")},
		{"屏幕 / Screen", fmt.Sprintf("%d × %d, %d dpi, %d Hz", r.ScreenWidth, r.ScreenHeight, r.DensityDpi, r.RefreshRate)},
		{"内存 / Memory", fmt.Sprintf("约 / Approximately %d GiB", r.MemoryGiB)},
		{"App", fmt.Sprintf("%s (%d)", r.AppVersion, r.AppVersionCode)},
		{"使用结果 / Compatibility", status},
	}
	var out strings.Builder
	fmt.Fprintf(&out, "## <code>%s</code> — %s\n\n", escape(r.MarketingName), status)
	out.WriteString("用户在 App 中确认提交。仅含白名单机型参数；未采集 IMEI、序列号、Android ID、账户、IP 或日志。\n\n")
	out.WriteString("Submitted with user confirmation in the app. Contains allowlisted model data only; no IMEI, serial, Android ID, account, IP or logs.\n\n")
	out.WriteString("| 参数 / Field | 值 / Value |\n| --- | --- |\n")
	for _, row := range rows {
		fmt.Fprintf(&out, "| %s | <code>%s</code> |\n", row[0], escape(row[1]))
	}
	fmt.Fprintf(&out, "\n<!-- azurpilot-device-report:%s -->\n", id)
	return out.String()
}
