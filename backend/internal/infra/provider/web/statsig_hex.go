package web

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// HEX 公式来自 grok 懒加载模块 1645e3（cdn.grok.com/_next/static/chunks/38asg_axwuaew.js，
// 文件名每次发版会变）。进程不会拉这个模块，也不会解混淆。生产签名使用冻住的
// (seed, HEX) 对，只刷新时间戳；不要用页面 curves 现算覆盖。
// 冻住的是「不要用错公式覆盖」，不是「一对 HEX 跨发版永远有效」。curves
// 变了就把 statsig_local.go 里的 pair 换成新的同一页抓包。
//
// 定位 1645e3：
//  1. 首页 JS 里搜 1645e3 或 x-statsig-id，调用链是
//     3mvtz9g_*.js 的 lk() → e.A(4629918) → 38asg_*.js。
//  2. 浏览器打开 grok.com/imagine，钩 crypto.subtle.digest，明文里
//     obfiowerehiring 后面就是 HEX；meta grok-site-verification 是 seed。
//  3. 钩 Element.animate / Animation.currentTime 和 getComputedStyle，
//     命中 duration=4096 的空 div：color+transform 编成 HEX。
//  4. 2026-09-09 模块 1645e3（chunk 0dz7o7zc0otoq.js）里：
//     路径 t[5]%4，段 W[39]%16，seek (W[3]%16)*(W[31]%16)*(W[36]%16)，
//     再 round(x/10)*10。旧下标 12 / 8,20,29 已失效。
//
// 下次 code 7 且 curves 已刷新仍失败时，用仓库技能 statsig-hex-repair
// （.grok/skills/statsig-hex-repair/）抓同一页对照并改下标。
// 旧 aurora 用 seed[5]%16 和 seed[22/23/24]，不要回退。

// 当前构建下的 4 条 Statsig SVG 路径。启动后若从 grok.com 页面 curves 抓到新路径会覆盖。
var (
	statsigSVGMu    sync.RWMutex
	statsigSVGPaths = [4]string{
		"M 10,30 C 246,151 99,203 53,79 h 102 s 54,18 101,133 C 140,11 231,100 171,82 h 123 s 244,146 64,177 C 11,91 248,139 191,108 h 126 s 22,229 116,117 C 129,248 48,73 72,146 h 113 s 67,164 242,155 C 170,245 188,26 48,72 h 123 s 53,34 199,77 C 235,141 95,206 34,166 h 80 s 238,187 13,246 C 203,236 148,247 169,74 h 189 s 186,62 161,5 C 204,218 248,19 125,111 h 94 s 46,74 61,233 C 16,153 135,254 32,144 h 93 s 136,57 87,36 C 6,168 243,113 8,46 h 26 s 125,124 140,11 C 11,119 134,16 161,155 h 111 s 239,164 40,71 C 85,237 152,42 17,37 h 207 s 59,53 3,158 C 234,176 213,138 105,68 h 202 s 201,117 12,96 C 191,105 216,147 86,148 h 165 s 0,99 202,200 C 229,108 175,166 241,120 h 52 s 137,97 166,42 C 191,128 220,123 61,232 h 173 s 97,142 36,62",
		"M 10,30 C 156,117 207,42 223,41 h 19 s 38,49 48,49 C 101,123 137,153 163,203 h 215 s 95,168 177,194 C 43,9 18,218 200,182 h 108 s 79,45 160,235 C 43,6 184,116 180,188 h 165 s 241,144 210,173 C 82,132 112,48 152,140 h 186 s 208,90 222,58 C 68,118 113,96 199,101 h 69 s 48,239 56,220 C 202,181 217,15 172,174 h 242 s 205,178 252,96 C 205,94 226,237 82,145 h 24 s 51,145 133,210 C 133,124 152,175 78,20 h 174 s 149,200 125,26 C 110,112 91,208 21,183 h 240 s 137,110 213,9 C 68,204 15,221 230,240 h 150 s 84,126 247,12 C 96,20 27,152 170,68 h 248 s 95,23 148,243 C 99,121 87,90 177,63 h 65 s 142,117 49,3 C 41,64 155,177 36,191 h 151 s 35,99 42,150 C 53,58 18,51 204,114 h 30 s 37,14 214,128 C 62,202 196,106 81,54 h 88 s 238,60 65,197",
		"M 10,30 C 31,67 4,145 10,113 h 196 s 34,213 178,180 C 166,229 107,13 244,222 h 29 s 152,19 106,12 C 206,0 254,226 59,219 h 22 s 92,26 131,9 C 51,100 227,144 46,61 h 230 s 141,199 120,238 C 70,59 61,163 79,222 h 254 s 126,149 31,42 C 162,47 102,81 141,1 h 26 s 116,163 19,128 C 249,167 236,174 150,21 h 30 s 70,69 175,221 C 33,214 0,175 19,181 h 115 s 87,69 145,184 C 220,165 77,79 153,8 h 35 s 240,95 50,105 C 94,208 36,170 250,216 h 190 s 94,104 54,18 C 67,42 12,227 205,149 h 42 s 130,4 90,224 C 227,77 200,69 148,137 h 175 s 118,155 71,160 C 173,142 220,201 136,114 h 31 s 181,51 225,124 C 60,154 86,165 37,127 h 10 s 211,233 249,172 C 103,253 82,11 126,119 h 113 s 73,94 79,116 C 130,48 62,47 117,232 h 36 s 246,42 86,218",
		"M 10,30 C 123,66 125,32 204,106 h 116 s 248,104 67,155 C 37,216 71,133 152,7 h 44 s 233,3 136,249 C 62,189 222,14 248,174 h 8 s 178,78 51,136 C 142,43 72,95 173,252 h 230 s 28,114 167,35 C 116,27 215,148 169,7 h 17 s 171,31 76,252 C 223,162 250,212 255,22 h 53 s 218,89 147,109 C 60,65 76,107 166,179 h 20 s 190,153 90,184 C 150,98 148,179 220,155 h 111 s 112,228 220,106 C 94,233 10,127 112,228 h 92 s 248,47 37,62 C 107,107 229,134 231,161 h 239 s 241,65 53,97 C 47,54 219,37 129,145 h 84 s 150,71 214,173 C 185,68 44,248 40,215 h 30 s 54,129 197,251 C 163,178 54,223 240,73 h 78 s 222,200 3,205 C 21,30 74,182 57,125 h 223 s 19,73 60,158 C 42,90 215,114 107,194 h 50 s 30,117 241,208 C 188,186 235,56 121,186 h 231 s 250,237 37,196",
	}
)

const statsigAnimationDuration = 4096.0

func replaceStatsigSVGPaths(paths []string) int {
	var next [4]string
	copied := 0
	for i := 0; i < 4 && i < len(paths); i++ {
		if strings.HasPrefix(strings.TrimSpace(paths[i]), "M 10,30 C") {
			next[i] = paths[i]
			copied++
		}
	}
	if copied < 4 {
		return 0
	}
	statsigSVGMu.Lock()
	statsigSVGPaths = next
	statsigSVGMu.Unlock()
	return copied
}

func computeStatsigHEXForSeed(seed []byte) (string, error) {
	statsigSVGMu.RLock()
	paths := statsigSVGPaths
	statsigSVGMu.RUnlock()
	return computeStatsigHEXForSeedWithPaths(seed, paths)
}

func computeStatsigHEXForSeedWithPaths(seed []byte, paths [4]string) (string, error) {
	if len(seed) < 30 {
		return "", fmt.Errorf("Statsig seed 过短")
	}
	path := paths[int(seed[5])%len(paths)]
	if path == "" {
		return "", fmt.Errorf("Statsig SVG 路径缺失")
	}
	segments := statsigPathNumberSegments(path)
	if len(segments) == 0 {
		return "", fmt.Errorf("Statsig SVG 路径无效")
	}
	// 1645e3：路径 seed[5]%4，段 seed[39]%16，seek (seed[3]%16)*(seed[31]%16)*(seed[36]%16) 再对齐到 10。
	if len(seed) < 40 {
		return "", fmt.Errorf("Statsig seed 过短")
	}
	segIdx := int(seed[39]) % 16
	if segIdx >= len(segments) {
		return "", fmt.Errorf("Statsig SVG 段越界")
	}
	seg := segments[segIdx]
	if len(seg) < 11 {
		return "", fmt.Errorf("Statsig SVG 段过短")
	}
	startColor := [3]float64{seg[0], seg[1], seg[2]}
	endColor := [3]float64{seg[3], seg[4], seg[5]}
	endAngle := statsigScaleValue(seg[6], 60, 360, true)
	x1 := statsigScaleValue(seg[7], 0, 1, false)
	y1 := statsigScaleValue(seg[8], -1, 1, false)
	x2 := statsigScaleValue(seg[9], 0, 1, false)
	y2 := statsigScaleValue(seg[10], -1, 1, false)
	seek := math.Round(float64((int(seed[3])%16)*(int(seed[31])%16)*(int(seed[36])%16))/10) * 10
	progress := statsigCubicBezierY(x1, y1, x2, y2, seek/statsigAnimationDuration)
	values := []float64{
		float64(statsigColorChannel(startColor[0], endColor[0], progress)),
		float64(statsigColorChannel(startColor[1], endColor[1], progress)),
		float64(statsigColorChannel(startColor[2], endColor[2], progress)),
		math.Cos(endAngle * progress * math.Pi / 180),
		math.Sin(endAngle * progress * math.Pi / 180),
	}
	values = append(values, -values[4], values[3], 0, 0)
	var buf strings.Builder
	for _, value := range values {
		buf.WriteString(statsigNumberToHex(statsigToFixed(value, 2)))
	}
	return strings.NewReplacer(".", "", "-", "").Replace(buf.String()), nil
}

func statsigPathNumberSegments(path string) [][]float64 {
	if len(path) <= 9 {
		return nil
	}
	parts := strings.Split(path[9:], "C")
	segments := make([][]float64, 0, len(parts))
	for _, part := range parts {
		nums := statsigExtractNumbers(part)
		if len(nums) > 0 {
			segments = append(segments, nums)
		}
	}
	return segments
}

func statsigExtractNumbers(seg string) []float64 {
	// 对齐 grok / aurora：seg.replace(/[^\d]+/g, " ")，点号和负号都是分隔符。
	var b strings.Builder
	inSpace := true
	for _, r := range seg {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			inSpace = false
			continue
		}
		if !inSpace {
			b.WriteByte(' ')
			inSpace = true
		}
	}
	parts := strings.Fields(b.String())
	nums := make([]float64, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			continue
		}
		nums = append(nums, value)
	}
	return nums
}

func statsigNumberToHex(n float64) string {
	if n == 0 {
		return "0"
	}
	if n == math.Trunc(n) && math.Abs(n) < 1<<53 {
		return strconv.FormatInt(int64(n), 16)
	}
	return statsigFloatToHexJS(n)
}

// statsigFloatToHexJS 对齐 JS Number.prototype.toString(16)。
func statsigFloatToHexJS(n float64) string {
	if n < 0 {
		return "-" + statsigFloatToHexJS(-n)
	}
	if n == 0 {
		return "0"
	}
	intPart := uint64(n)
	var buf strings.Builder
	buf.WriteString(strconv.FormatUint(intPart, 16))
	frac := n - float64(intPart)
	if frac > 0 {
		digits := make([]byte, 0, 16)
		for i := 0; i < 16 && frac > 1e-18; i++ {
			frac *= 16
			digit := uint64(frac + 1e-12)
			if digit > 15 {
				digit = 15
			}
			digits = append(digits, "0123456789abcdef"[digit])
			frac -= float64(digit)
			if frac < 0 {
				frac = 0
			}
		}
		for len(digits) > 0 && digits[len(digits)-1] == '0' {
			digits = digits[:len(digits)-1]
		}
		if len(digits) > 0 {
			buf.WriteByte('.')
			buf.Write(digits)
		}
	}
	return buf.String()
}

func statsigScaleValue(n, min, max float64, floor bool) float64 {
	v := n*((max-min)/255) + min
	if floor {
		return math.Floor(v)
	}
	return statsigToFixed(v, 2)
}

func statsigColorChannel(start, end, progress float64) int {
	v := math.Round(start + (end-start)*progress)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v)
}

func statsigToFixed(v float64, precision int) float64 {
	pow := math.Pow10(precision)
	return math.Round(v*pow) / pow
}

func statsigCubicBezierY(x1, y1, x2, y2, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	t := x
	for i := 0; i < 8; i++ {
		xAtT := statsigSampleCubic(t, x1, x2) - x
		if math.Abs(xAtT) < 1e-7 {
			return statsigSampleCubic(t, y1, y2)
		}
		d := statsigSampleCubicDerivative(t, x1, x2)
		if math.Abs(d) < 1e-7 {
			break
		}
		t -= xAtT / d
	}
	lo, hi := 0.0, 1.0
	t = x
	for lo < hi {
		xAtT := statsigSampleCubic(t, x1, x2)
		if math.Abs(xAtT-x) < 1e-7 {
			return statsigSampleCubic(t, y1, y2)
		}
		if x > xAtT {
			lo = t
		} else {
			hi = t
		}
		t = (hi + lo) / 2
	}
	return statsigSampleCubic(t, y1, y2)
}

func statsigSampleCubic(t, a1, a2 float64) float64 {
	return ((1-3*a2+3*a1)*t+(3*a2-6*a1))*t*t + 3*a1*t
}

func statsigSampleCubicDerivative(t, a1, a2 float64) float64 {
	return (3*(1-3*a2+3*a1)*t+2*(3*a2-6*a1))*t + 3*a1
}
