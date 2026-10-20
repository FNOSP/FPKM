package web

import (
	"strconv"
	"strings"
)

// compareVersion 比较两个版本号。
// 返回 >0 表示 a 比 b 新，<0 表示更旧，0 表示相同。
// 支持 "1.2.3"、"1.2.3-beta"、"v1.2" 等常见形式；无法解析时退化为字符串比较。
func compareVersion(a, b string) int {
	an := normalizeVersion(a)
	bn := normalizeVersion(b)

	// 逐段比较数字
	for i := 0; i < len(an.nums) || i < len(bn.nums); i++ {
		var x, y int
		if i < len(an.nums) {
			x = an.nums[i]
		}
		if i < len(bn.nums) {
			y = bn.nums[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}

	// 数字段相同，比较预发布标识：有预发布标识的视为更旧
	switch {
	case an.pre == "" && bn.pre == "":
		return 0
	case an.pre == "":
		return 1
	case bn.pre == "":
		return -1
	default:
		return strings.Compare(an.pre, bn.pre)
	}
}

type versionParts struct {
	nums []int
	pre  string
}

// normalizeVersion 拆出数字段与预发布标识
func normalizeVersion(v string) versionParts {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	// 以 + 分隔的构建元数据忽略
	if idx := strings.IndexByte(v, '+'); idx >= 0 {
		v = v[:idx]
	}

	pre := ""
	// 以 - 分隔的预发布标识（如 1.2.3-beta.1）
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		pre = v[idx+1:]
		v = v[:idx]
	}

	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '_'
	})

	var nums []int
	for _, f := range fields {
		// 段内可能混有非数字（如 "3a"），只取前导数字
		numStr := leadingDigits(f)
		if numStr == "" {
			nums = append(nums, 0)
			continue
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			nums = append(nums, 0)
			continue
		}
		nums = append(nums, n)
	}

	return versionParts{nums: nums, pre: pre}
}

func leadingDigits(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}
