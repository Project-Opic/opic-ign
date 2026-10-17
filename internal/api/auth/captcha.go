// 图形验证码 —— 本地登录安控加强。
//
// 纯标准库实现:4 位随机码(去掉易混淆字符),大小写不敏感;
// 答案只以像素图(PNG)形态随响应下发 —— 字形是服务端 5x7 点阵放大后的像素,
// 不存在任何文本节点/属性可机械还原答案(2026-10 审计修复:原 SVG 文本节点即答案)。
// 校验与领取来源绑定,领取侧按来源限速;验证码一次性,5 分钟过期,答错即作废。
// SSO(Casdoor/ZITADEL)跳转登录不受影响。
package auth

import (
	"bytes"
	crand "crypto/rand"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/common"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

const captchaTTL = 5 * time.Minute

// captchaEntry 一条待验证的验证码。ip 记录领取来源,校验时要求一致(来源绑定)。
type captchaEntry struct {
	answer  string
	expires time.Time
	ip      string
}

var captchaStore = struct {
	sync.Mutex
	m map[string]captchaEntry
}{m: map[string]captchaEntry{}}

// captchaCharset 去掉 0/O/1/I/l 等易混淆字符。
const captchaCharset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// ---------- 反自动化节流(服务端进程内;多副本部署由入口网关再叠加) ----------

// 节流参数:同一来源每分钟领取验证码上限、登录失败窗口上限(按来源/来源+账号)。
// 数值可按部署压测结论调整;触发后请求在窗口内一律拒绝(失败退避)。
var (
	captchaIssue  = newRateWindow(30, time.Minute)                   // 同一来源每分钟领取验证码上限
	loginFailIP   = newRateWindow(10, 5*time.Minute)                 // 同一来源失败上限
	loginFailPair = newRateWindow(loginFailPairLimit, 5*time.Minute) // 同来源+账号失败上限
)

// loginFailPairLimit 同来源+账号的失败上限(与 loginFailPair 的 limit 保持同源)。
const loginFailPairLimit = 5

// rateWindow 滑动窗口计数:窗口内计数达到 limit 即判超限;持续攻击不无限追加(上限保护)。
type rateWindow struct {
	mu     sync.Mutex
	m      map[string][]time.Time
	limit  int
	window time.Duration
}

func newRateWindow(limit int, window time.Duration) *rateWindow {
	return &rateWindow{m: map[string][]time.Time{}, limit: limit, window: window}
}

// exceeded 判定窗口内计数是否已达上限(只读判定,不记录)。
func (w *rateWindow) exceeded(key string) bool {
	cut := time.Now().Add(-w.window)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.m[key] = pruneTimes(w.m[key], cut)
	return len(w.m[key]) >= w.limit
}

// record 记录一次事件并返回窗口内计数。
func (w *rateWindow) record(key string) int {
	cut := time.Now().Add(-w.window)
	w.mu.Lock()
	defer w.mu.Unlock()
	ev := pruneTimes(w.m[key], cut)
	if len(ev) < w.limit*4 { // 上限保护:持续攻击不再无限追加,防内存放大
		ev = append(ev, time.Now())
	}
	w.m[key] = ev
	if len(w.m) > 4096 { // 防内存无界增长:条目过多时全量清扫过期键
		for k, v := range w.m {
			if v = pruneTimes(v, cut); len(v) == 0 {
				delete(w.m, k)
			} else {
				w.m[k] = v
			}
		}
	}
	return len(ev)
}

// reset 清空某键的窗口(登录成功后重置该账号的失败计数)。
func (w *rateWindow) reset(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.m, key)
}

// pruneTimes 丢弃窗口外的事件(原地压缩,读写下标安全)。
func pruneTimes(src []time.Time, cut time.Time) []time.Time {
	out := src[:0]
	for _, t := range src {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	return out
}

// Captcha 下发图形验证码:GET /api/auth/captcha → {captcha_id, image}。
// 答案只以 PNG 像素图交付;按来源限速,防脚本无限重领。
func Captcha(c *gin.Context) {
	ip := c.ClientIP()
	if captchaIssue.exceeded("ip:" + ip) {
		c.JSON(200, errcode.New(errcode.ErrLogin, "验证码领取过于频繁，请稍后再试", nil))
		return
	}
	captchaIssue.record("ip:" + ip)

	code := randomCode(4)
	id := randomCode(16) + fmtTime()
	captchaStore.Lock()
	// 顺手清理过期项,防止内存无界增长
	now := time.Now()
	for k, e := range captchaStore.m {
		if now.After(e.expires) {
			delete(captchaStore.m, k)
		}
	}
	captchaStore.m[id] = captchaEntry{answer: code, expires: now.Add(captchaTTL), ip: ip}
	captchaStore.Unlock()

	pix := renderCaptchaPNG(code)
	common.OKJSON(c, gin.H{
		"captcha_id": id,
		"image":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(pix),
		"ttl":        int(captchaTTL.Seconds()),
	}, "")
}

// VerifyCaptcha 校验并销毁(一次性)。id/code/来源为空直接 false;
// 验证码只能由领取它的同一来源提交(限制跨来源转发与批量机器解题)。
func VerifyCaptcha(id, code, ip string) bool {
	if id == "" || code == "" || ip == "" {
		return false
	}
	captchaStore.Lock()
	e, ok := captchaStore.m[id]
	if ok {
		delete(captchaStore.m, id) // 无论对错都销毁,防重放
	}
	captchaStore.Unlock()
	if !ok || time.Now().After(e.expires) {
		return false
	}
	if e.ip == "" || e.ip != ip {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(code), e.answer)
}

func randomCode(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(captchaCharset)))
	for i := range b {
		v, err := crand.Int(crand.Reader, max)
		if err != nil {
			v = big.NewInt(time.Now().UnixNano() % max.Int64())
		}
		b[i] = captchaCharset[v.Int64()]
	}
	return string(b)
}

func fmtTime() string { return strings.ToLower(randBase32(time.Now().UnixNano())) }

func randBase32(v int64) string {
	const digits = "0123456789abcdefghijklmnopqrstuv"
	out := make([]byte, 0, 12)
	for ; v > 0; v >>= 5 {
		out = append(out, digits[v&31])
	}
	return string(out)
}

// randIntn 密码学随机 [0,n)(图像抖动/噪声同样取 CSPRNG,不依赖全局 PRNG 状态)。
func randIntn(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := crand.Int(crand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// palette 低饱和底色,与登录页白底蓝字风格一致。
var palette = []string{"#e8f1fd", "#eef7ee", "#fdf3e7", "#f3eefb"}

// colors 字符与干扰线的取色域。
var colors = []string{"#4c7dd0", "#5aa35a", "#c9803e", "#8a6fc0"}

// captchaGlyphs 5x7 点阵字形(仅覆盖 captchaCharset;'#' 为笔画像素)。
// 点阵放大后只以像素交付,不引入字体/图像库依赖。
var captchaGlyphs = map[rune][7]string{
	'2': {".####", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	'3': {".####", "#...#", "....#", "..##.", "....#", "#...#", ".####"},
	'4': {"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	'5': {"#####", "#....", "####.", "....#", "....#", "#...#", ".###."},
	'6': {"..##.", ".#...", "#....", "####.", "#...#", "#...#", ".###."},
	'7': {"#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."},
	'8': {".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."},
	'9': {".###.", "#...#", "#...#", ".####", "....#", "...#.", ".##.."},
	'A': {"..#..", ".#.#.", "#...#", "#...#", "#####", "#...#", "#...#"},
	'B': {"####.", "#...#", "#...#", "####.", "#...#", "#...#", "####."},
	'C': {".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."},
	'D': {"###..", "#..#.", "#...#", "#...#", "#...#", "#..#.", "###.."},
	'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
	'F': {"#####", "#....", "#....", "####.", "#....", "#....", "#...."},
	'G': {".###.", "#...#", "#....", "#.###", "#...#", "#...#", ".###."},
	'H': {"#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'J': {"..###", "...#.", "...#.", "...#.", "...#.", "#..#.", ".##.."},
	'K': {"#...#", "#..#.", "#.#..", "##...", "#.#..", "#..#.", "#...#"},
	'M': {"#...#", "##.##", "#.#.#", "#.#.#", "#...#", "#...#", "#...#"},
	'N': {"#...#", "##..#", "#.#.#", "#.#.#", "#..##", "#...#", "#...#"},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'Q': {".###.", "#...#", "#...#", "#...#", "#.#.#", "#..#.", ".##.#"},
	'R': {"####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"},
	'S': {".####", "#....", "#....", ".###.", "....#", "....#", "####."},
	'T': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."},
	'U': {"#...#", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'V': {"#...#", "#...#", "#...#", "#...#", "#...#", ".#.#.", "..#.."},
	'W': {"#...#", "#...#", "#...#", "#.#.#", "#.#.#", "##.##", "#...#"},
	'X': {"#...#", "#...#", ".#.#.", "..#..", ".#.#.", "#...#", "#...#"},
	'Y': {"#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."},
	'Z': {"#####", "....#", "...#.", "..#..", ".#...", "#....", "#####"},
	'?': {".###.", "#...#", "....#", "..##.", "..#..", ".....", "..#.."},
}

// renderCaptchaPNG 把答案渲染为 PNG 像素图:5x7 点阵放大 + 逐字符斜切/基线抖动 +
// 随机噪声线/噪点;坐标与噪声全随机(不再由答案推导),答案只能经视觉识别恢复。
func renderCaptchaPNG(code string) []byte {
	const w, h, scale = 120, 40, 4
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fillRect(img, hexColor(palette[randIntn(len(palette))]))
	for k := 0; k < 3; k++ { // 干扰线
		drawLine(img, randIntn(w), randIntn(h), randIntn(w), randIntn(h),
			hexColor(colors[randIntn(len(colors))]))
	}
	for k := 0; k < 80; k++ { // 噪点
		img.SetNRGBA(randIntn(w), randIntn(h), hexColor(colors[randIntn(len(colors))]))
	}
	for i, ch := range code {
		glyph, ok := captchaGlyphs[ch]
		if !ok {
			glyph = captchaGlyphs['?']
		}
		x0 := 6 + i*28 + randIntn(4)
		y0 := 3 + randIntn(6)
		shear := randIntn(5) - 2 // -2..2:逐字符斜切,增加 OCR 成本
		fg := hexColor(colors[i%len(colors)])
		for gy := 0; gy < 7; gy++ {
			for gx := 0; gx < 5; gx++ {
				if glyph[gy][gx] != '#' {
					continue
				}
				for py := 0; py < scale; py++ {
					for px := 0; px < scale; px++ {
						x := x0 + gx*scale + px + gy*shear/2
						y := y0 + gy*scale + py
						if x >= 0 && x < w && y >= 0 && y < h {
							img.SetNRGBA(x, y, fg)
						}
					}
				}
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// fillRect 以底色铺满画布。
func fillRect(img *image.NRGBA, col color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetNRGBA(x, y, col)
		}
	}
}

// drawLine 画一条噪声线(逐点插值;越界像素丢弃)。
func drawLine(img *image.NRGBA, x1, y1, x2, y2 int, col color.NRGBA) {
	dx, dy := x2-x1, y2-y1
	steps := dx
	if steps < 0 {
		steps = -steps
	}
	if dy < 0 {
		if -dy > steps {
			steps = -dy
		}
	} else if dy > steps {
		steps = dy
	}
	if steps == 0 {
		steps = 1
	}
	for k := 0; k <= steps; k++ {
		x, y := x1+dx*k/steps, y1+dy*k/steps
		if image.Pt(x, y).In(img.Bounds()) {
			img.SetNRGBA(x, y, col)
		}
	}
}

// hexColor #rrggbb → 颜色。
func hexColor(s string) color.NRGBA {
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{A: 255}
	}
	digit := func(c byte) uint8 {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10
		}
		return 0
	}
	return color.NRGBA{
		R: digit(s[1])<<4 | digit(s[2]),
		G: digit(s[3])<<4 | digit(s[4]),
		B: digit(s[5])<<4 | digit(s[6]),
		A: 255,
	}
}
