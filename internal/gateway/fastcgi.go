package gateway

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// FastCGI 记录类型
const (
	typeBeginRequest    uint8 = 1
	typeAbortRequest    uint8 = 2
	typeEndRequest      uint8 = 3
	typeParams          uint8 = 4
	typeStdin           uint8 = 5
	typeStdout          uint8 = 6
	typeStderr          uint8 = 7
	typeData            uint8 = 8
	typeGetValues       uint8 = 9
	typeGetValuesResult uint8 = 10
	typeUnknownType     uint8 = 11
)

// FastCGI 角色
const (
	roleResponder uint16 = 1
)

// FastCGI 协议版本
const fastcgiVersion uint8 = 1

// fcgiHeader FastCGI 记录头部 (8 字节)
type fcgiHeader struct {
	Version       uint8
	Type          uint8
	RequestID     uint16
	ContentLength uint16
	PaddingLength uint8
	Reserved      uint8
}

// writeHeader 写入 FastCGI 记录头
func writeHeader(w io.Writer, recType uint8, requestID uint16, contentLength int) error {
	h := fcgiHeader{
		Version:       fastcgiVersion,
		Type:          recType,
		RequestID:     requestID,
		ContentLength: uint16(contentLength),
		PaddingLength: uint8((-contentLength) & 7),
	}
	var buf [8]byte
	buf[0] = h.Version
	buf[1] = h.Type
	buf[2] = byte(h.RequestID >> 8)
	buf[3] = byte(h.RequestID)
	buf[4] = byte(h.ContentLength >> 8)
	buf[5] = byte(h.ContentLength)
	buf[6] = h.PaddingLength
	buf[7] = h.Reserved
	_, err := w.Write(buf[:])
	return err
}

// writeRecord 写入一条完整的 FastCGI 记录
func writeRecord(w io.Writer, recType uint8, requestID uint16, content []byte) error {
	if err := writeHeader(w, recType, requestID, len(content)); err != nil {
		return err
	}
	if len(content) > 0 {
		if _, err := w.Write(content); err != nil {
			return err
		}
	}
	// 填充 padding
	padding := (-len(content)) & 7
	if padding > 0 {
		var pad [8]byte
		_, err := w.Write(pad[:padding])
		return err
	}
	return nil
}

// writeBeginRequest 写入 BEGIN_REQUEST 记录
func writeBeginRequest(w io.Writer, requestID uint16, role uint16, keepConn bool) error {
	var body [8]byte
	body[0] = byte(role >> 8)
	body[1] = byte(role)
	if keepConn {
		body[2] = 1
	}
	return writeRecord(w, typeBeginRequest, requestID, body[:])
}

// writePairs 写入键值对（PARAMS 或 GET_VALUES）
// FastCGI 键值对编码：
//   - 长度 < 128: 1 字节长度 + 内容
//   - 长度 >= 128: 4 字节长度（最高位为 1）+ 内容
func writePairs(w io.Writer, requestID uint16, recType uint8, pairs map[string]string) error {
	var buf bytes.Buffer
	for k, v := range pairs {
		keyLen := len(k)
		valLen := len(v)

		// key 长度
		if keyLen < 128 {
			buf.WriteByte(byte(keyLen))
		} else {
			buf.WriteByte(byte(keyLen>>24) | 0x80)
			buf.WriteByte(byte(keyLen >> 16))
			buf.WriteByte(byte(keyLen >> 8))
			buf.WriteByte(byte(keyLen))
		}
		// value 长度
		if valLen < 128 {
			buf.WriteByte(byte(valLen))
		} else {
			buf.WriteByte(byte(valLen>>24) | 0x80)
			buf.WriteByte(byte(valLen >> 16))
			buf.WriteByte(byte(valLen >> 8))
			buf.WriteByte(byte(valLen))
		}
		buf.WriteString(k)
		buf.WriteString(v)
	}
	return writeRecord(w, recType, requestID, buf.Bytes())
}

// writeEmptyParams 写入空的 PARAMS 记录（表示参数结束）
func writeEmptyParams(w io.Writer, requestID uint16) error {
	return writeRecord(w, typeParams, requestID, nil)
}

// readHeader 读取 FastCGI 记录头
func readHeader(r io.Reader) (fcgiHeader, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return fcgiHeader{}, err
	}
	return fcgiHeader{
		Version:       buf[0],
		Type:          buf[1],
		RequestID:     uint16(buf[2])<<8 | uint16(buf[3]),
		ContentLength: uint16(buf[4])<<8 | uint16(buf[5]),
		PaddingLength: buf[6],
		Reserved:      buf[7],
	}, nil
}

// readRecord 读取一条 FastCGI 记录的内容
func readRecord(r io.Reader, h fcgiHeader) ([]byte, error) {
	content := make([]byte, h.ContentLength)
	if _, err := io.ReadFull(r, content); err != nil {
		return nil, err
	}
	// 跳过 padding
	if h.PaddingLength > 0 {
		padding := make([]byte, h.PaddingLength)
		io.ReadFull(r, padding)
	}
	return content, nil
}

// FastCGIClient FastCGI 客户端
// 用于通过 FastCGI 协议与后端通信（如 PHP-FPM、飞牛 trim_http_cgi 等）
//
// 对应飞牛 nginx 的 fastcgi_pass 指令，模拟 nginx 的 FastCGI 转发行为。
type FastCGIClient struct {
	network string // "unix" 或 "tcp"
	address string // socket 路径或 host:port
}

// NewFastCGIClient 创建 FastCGI 客户端
// network: "unix"（Unix Socket）或 "tcp"（TCP 连接）
// address: socket 路径或 host:port
func NewFastCGIClient(network, address string) *FastCGIClient {
	return &FastCGIClient{
		network: network,
		address: address,
	}
}

// Do 执行一次 FastCGI 请求
//
// 将 HTTP 请求转换为 FastCGI 请求发送给后端，
// 再将 FastCGI 响应转换为 HTTP 响应写回客户端。
//
// 这是 fpkm 网关模拟飞牛 trim_http_cgi 的核心函数。
// 飞牛中 /cgi 路径的请求通过 fastcgi_pass 转发到 trim_http_cgi.sock，
// 这里用 Go 实现了同样的 FastCGI 协议转换。
func (c *FastCGIClient) Do(w http.ResponseWriter, r *http.Request, scriptFilename string) error {
	conn, err := net.DialTimeout(c.network, c.address, 10*time.Second)
	if err != nil {
		return fmt.Errorf("连接 FastCGI 后端失败: %w", err)
	}
	defer conn.Close()

	// 设置超时
	conn.SetDeadline(time.Now().Add(5 * time.Minute))

	const requestID uint16 = 1

	// 1. 发送 BEGIN_REQUEST
	if err := writeBeginRequest(conn, requestID, roleResponder, false); err != nil {
		return fmt.Errorf("发送 BEGIN_REQUEST 失败: %w", err)
	}

	// 2. 构建 FastCGI 参数（CGI 环境变量）
	params := buildFastCGIParams(r, scriptFilename)
	if err := writePairs(conn, requestID, typeParams, params); err != nil {
		return fmt.Errorf("发送 PARAMS 失败: %w", err)
	}
	// 发送空 PARAMS 表示参数结束
	if err := writeEmptyParams(conn, requestID); err != nil {
		return fmt.Errorf("发送空 PARAMS 失败: %w", err)
	}

	// 3. 发送请求体（STDIN）
	if r.Body != nil && r.ContentLength > 0 {
		// 流式发送请求体，分块发送（每块最大 65535 字节）
		buf := make([]byte, 65535)
		remaining := r.ContentLength
		for remaining > 0 {
			n, err := r.Body.Read(buf)
			if n > 0 {
				if err := writeRecord(conn, typeStdin, requestID, buf[:n]); err != nil {
					return fmt.Errorf("发送 STDIN 失败: %w", err)
				}
				remaining -= int64(n)
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("读取请求体失败: %w", err)
			}
		}
	}
	// 发送空 STDIN 表示请求体结束
	if err := writeRecord(conn, typeStdin, requestID, nil); err != nil {
		return fmt.Errorf("发送空 STDIN 失败: %w", err)
	}

	// 4. 读取响应
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer

	for {
		h, err := readHeader(conn)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("读取响应头失败: %w", err)
		}

		content, err := readRecord(conn, h)
		if err != nil {
			return fmt.Errorf("读取响应内容失败: %w", err)
		}

		switch h.Type {
		case typeStdout:
			stdoutBuf.Write(content)
		case typeStderr:
			stderrBuf.Write(content)
		case typeEndRequest:
			// 请求结束
			goto done
		case typeGetValuesResult:
			// 忽略
		default:
			// 忽略未知类型
		}
	}
done:

	// 如果有 stderr 输出，记日志
	if stderrBuf.Len() > 0 {
		// stderr 是后端的错误输出，不影响正常响应
	}

	// 5. 解析 CGI 响应并写入 HTTP 响应
	// CGI 响应格式：头部（Header: value\r\n） + 空行 + 响应体
	return writeCGIResponse(w, &stdoutBuf)
}

// buildFastCGIParams 构建 FastCGI 参数（CGI 环境变量）
// 模拟 nginx 的 fastcgi_params 配置
func buildFastCGIParams(r *http.Request, scriptFilename string) map[string]string {
	params := make(map[string]string)

	// 基础 CGI 变量
	params["GATEWAY_INTERFACE"] = "CGI/1.1"
	params["SERVER_PROTOCOL"] = "HTTP/1.1"
	params["REQUEST_METHOD"] = r.Method
	params["REQUEST_URI"] = r.URL.RequestURI()
	params["QUERY_STRING"] = r.URL.RawQuery
	params["DOCUMENT_URI"] = r.URL.Path
	params["SCRIPT_NAME"] = r.URL.Path
	params["SCRIPT_FILENAME"] = scriptFilename
	params["REQUEST_SCHEME"] = "http"

	// Content-Type / Content-Length
	if ct := r.Header.Get("Content-Type"); ct != "" {
		params["CONTENT_TYPE"] = ct
	}
	if r.ContentLength > 0 {
		params["CONTENT_LENGTH"] = strconv.FormatInt(r.ContentLength, 10)
	}

	// 服务器信息
	params["SERVER_SOFTWARE"] = "fpkm-gateway/1.0"
	params["SERVER_NAME"] = r.Host
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
		port = "80"
	}
	params["SERVER_ADDR"] = host
	params["SERVER_PORT"] = port

	// 客户端信息
	remoteHost, remotePort, _ := net.SplitHostPort(r.RemoteAddr)
	params["REMOTE_ADDR"] = remoteHost
	params["REMOTE_PORT"] = remotePort
	if r.TLS != nil {
		params["HTTPS"] = "on"
	}

	// HTTP 头 -> HTTP_* 变量
	for k, v := range r.Header {
		name := "HTTP_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
		params[name] = strings.Join(v, ", ")
	}

	return params
}

// writeCGIResponse 解析 CGI 响应并写入 http.ResponseWriter
//
// CGI 响应格式（RFC 3875）：
//
//	Content-Type: text/html\r\n
//	Status: 200 OK\r\n
//	X-Custom-Header: value\r\n
//	\r\n
//	<body>
//
// 特殊情况：
//   - 如果有 Location 头且没有 Status，默认 302 重定向
//   - 如果没有 Status 头，默认 200
//   - 如果第一行是 "HTTP/1.x ..."，解析为完整 HTTP 响应（nph 脚本）
func writeCGIResponse(w http.ResponseWriter, stdout *bytes.Buffer) error {
	if stdout.Len() == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}

	// 用 bufio.Reader 逐行读取头部
	reader := bufio.NewReader(stdout)
	tp := textproto.NewReader(reader)

	// 检查是否是完整 HTTP 响应（nph 脚本）
	firstLine, err := tp.ReadLine()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("读取响应首行失败: %w", err)
	}

	statusCode := http.StatusOK
	headers := make(http.Header)

	// 如果是 HTTP 响应行
	if strings.HasPrefix(firstLine, "HTTP/") {
		// 解析状态码
		parts := strings.SplitN(firstLine, " ", 3)
		if len(parts) >= 2 {
			if code, err := strconv.Atoi(parts[1]); err == nil {
				statusCode = code
			}
		}
	} else {
		// 普通 CGI 响应，首行是头部
		if err := parseCGIHeaderLine(firstLine, headers); err == nil {
			if s := headers.Get("Status"); s != "" {
				if code, _, ok := parseStatus(s); ok {
					statusCode = code
				}
			}
		}
	}

	// 读取剩余头部
	for {
		line, err := tp.ReadLine()
		if err != nil {
			break
		}
		if line == "" {
			break // 空行分隔头部和响应体
		}
		if err := parseCGIHeaderLine(line, headers); err != nil {
			continue
		}
	}

	// 处理 Status 头
	if s := headers.Get("Status"); s != "" {
		if code, _, ok := parseStatus(s); ok {
			statusCode = code
		}
		headers.Del("Status")
	}

	// 处理 Location 头（重定向）
	if location := headers.Get("Location"); location != "" && statusCode == http.StatusOK {
		statusCode = http.StatusFound
	}

	// 写入头部
	for k, values := range headers {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}

	// 写入状态码
	w.WriteHeader(statusCode)

	// 写入响应体
	_, err = io.Copy(w, reader)
	return err
}

// parseCGIHeaderLine 解析一行 CGI 头部 "Key: value"
func parseCGIHeaderLine(line string, headers http.Header) error {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return fmt.Errorf("无效头部行: %s", line)
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	headers.Add(key, val)
	return nil
}

// parseStatus 解析 Status 头的值，如 "200 OK" → (200, "OK", true)
func parseStatus(s string) (int, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), " ", 2)
	if len(parts) == 0 {
		return 0, "", false
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	reason := ""
	if len(parts) > 1 {
		reason = parts[1]
	}
	return code, reason, true
}

// FastCGITransport 实现 http.RoundTripper 接口，通过 FastCGI 协议转发 HTTP 请求
// 这样可以直接集成到 httputil.ReverseProxy 中使用
type FastCGITransport struct {
	Network        string // "unix" 或 "tcp"
	Address        string // socket 路径或 host:port
	ScriptFilename string // SCRIPT_FILENAME 环境变量值
}

// RoundTrip 实现 http.RoundTripper 接口
func (t *FastCGITransport) RoundTrip(r *http.Request) (*http.Response, error) {
	conn, err := net.DialTimeout(t.Network, t.Address, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Minute))

	const requestID uint16 = 1

	// BEGIN_REQUEST
	if err := writeBeginRequest(conn, requestID, roleResponder, false); err != nil {
		return nil, err
	}

	// PARAMS
	params := buildFastCGIParams(r, t.ScriptFilename)
	if err := writePairs(conn, requestID, typeParams, params); err != nil {
		return nil, err
	}
	if err := writeEmptyParams(conn, requestID); err != nil {
		return nil, err
	}

	// STDIN
	if r.Body != nil {
		buf := make([]byte, 65535)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				if err := writeRecord(conn, typeStdin, requestID, buf[:n]); err != nil {
					return nil, err
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return nil, err
			}
		}
	}
	if err := writeRecord(conn, typeStdin, requestID, nil); err != nil {
		return nil, err
	}

	// 读取响应
	var stdoutBuf bytes.Buffer
	for {
		h, err := readHeader(conn)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		content, err := readRecord(conn, h)
		if err != nil {
			return nil, err
		}
		if h.Type == typeStdout {
			stdoutBuf.Write(content)
		} else if h.Type == typeEndRequest {
			break
		}
	}

	// 解析为 http.Response
	return parseCGIHTTPResponse(r, &stdoutBuf)
}

// parseCGIHTTPResponse 将 CGI 响应解析为 *http.Response
func parseCGIHTTPResponse(r *http.Request, stdout *bytes.Buffer) (*http.Response, error) {
	// 用 httptest 伪造一个响应记录器来解析
	rec := httptest.NewRecorder()
	if err := writeCGIResponse(rec, stdout); err != nil {
		return nil, err
	}

	resp := &http.Response{
		StatusCode:    rec.Code,
		Header:        rec.Header(),
		Body:          io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		ContentLength: int64(rec.Body.Len()),
		Request:       r,
	}
	return resp, nil
}
