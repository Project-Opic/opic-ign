// ign_mask.go —— 日志行写前不可逆脱敏（R-16 / INV-40；REF-12 超长截断）。
//
// 口径：掩码发生在落库之前，界面视图与门面回吐读的是同一份已脱敏数据 —— 不存在第二条
// 未脱敏通道。掩码为字面替换、原文不留存；宁可过掩，不可漏掩（幂等：命中的掩码标记不再二次替换）。
package service

import (
	"regexp"
	"strings"
)

// ignMaskMark 掩码标记（落库文本的唯一替换符）。
const ignMaskMark = "[已掩码]"

// ignLogLineMaxRunes 单行落库上限（REF-12 超长截断并标注）。
const ignLogLineMaxRunes = 4000

var (
	ignPemRe = regexp.MustCompile(`-----BEGIN [^-\n]*?-----`)
	// 赋值形态：口令 / 令牌 / 私钥 / 证书数据 / kubeconfig 字段（ezctl 与 ansible 输出常见）。
	// 键名允许下划线/点/连字符前缀 —— root_token、bootstrap_token、etcd.passwd 这类合成键名
	// 中间没有词边界，只匹配裸词会漏掩（宁可过掩，不可漏掩）。
	ignCredRe = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(password|passwd|pwd|token|secret|client_secret|client-key-data|client-certificate-data|certificate-authority-data|certificate-authorisation-data|authorization|access_key|access-key|user_token))\b(\s*[:=]\s*)([^\s,'"]+)`)
	// 命令行选项形态：--password xxx / -p xxx。
	ignOptRe  = regexp.MustCompile(`(?i)(^|\s)((?:--password|--passwd|--token|--secret|-p)\s+)('[^']*'|"[^"]*"|\S+)`)
	ignBearRe = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9\-._~+/]{8,}={0,2}`)
	ignJwtRe  = regexp.MustCompile(`\b[A-Za-z0-9._-]{24,}\.[A-Za-z0-9._-]{24,}\.[A-Za-z0-9._-]{10,}\b`)
	// 裸 base64 大块（私钥/证书体逐行输出时的形态；正常进度输出不会达到该长度）。
	ignB64Re = regexp.MustCompile(`\b[A-Za-z0-9+/]{60,}={0,2}\b`)
)

// ignMaskLine 对单行执行不可逆掩码，并回传"是否命中掩码规则"（TaskLogLine.masked）。
func ignMaskLine(line string) (string, bool) {
	out := ignPemRe.ReplaceAllString(line, ignMaskMark)
	out = ignOptRe.ReplaceAllStringFunc(out, func(m string) string {
		g := ignOptRe.FindStringSubmatch(m)
		if strings.TrimSpace(g[3]) == ignMaskMark {
			return m
		}
		return g[1] + g[2] + ignMaskMark
	})
	out = ignCredRe.ReplaceAllStringFunc(out, func(m string) string {
		g := ignCredRe.FindStringSubmatch(m)
		if strings.EqualFold(g[4], "bearer") {
			return m // 交由 bearer 规则连同令牌一并掩码，避免双重标记
		}
		if strings.TrimSpace(g[4]) == ignMaskMark {
			return m
		}
		return g[1] + g[3] + ignMaskMark
	})
	out = ignBearRe.ReplaceAllStringFunc(out, func(m string) string {
		g := ignBearRe.FindStringSubmatch(m)
		if strings.TrimSpace(strings.TrimPrefix(m, g[1])) == ignMaskMark {
			return m
		}
		return g[1] + " " + ignMaskMark
	})
	out = ignJwtRe.ReplaceAllString(out, ignMaskMark)
	out = ignB64Re.ReplaceAllString(out, ignMaskMark)
	return out, out != line
}

// ignTruncateLogLine 超长截断并标注（REF-12）。
func ignTruncateLogLine(line string) string {
	r := []rune(line)
	if len(r) <= ignLogLineMaxRunes {
		return line
	}
	return string(r[:ignLogLineMaxRunes]) + "……（超长已截断，REF-12）"
}
