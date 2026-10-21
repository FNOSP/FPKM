package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// 上传限制
const (
	// maxUploadBytes 单个上传包的体积上限（2 GiB），防止磁盘被塞满
	maxUploadBytes = int64(2) << 30
	// uploadMemory 多段表单内存缓冲上限
	uploadMemory = int64(32) << 20
)

// handleInstallLocal POST /api/install/local  {path, set: {k:v}}
// 从服务器本地已有的 .fpk 文件安装
func (s *Server) handleInstallLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !s.opts.AllowFS {
		writeJSONError(w, http.StatusForbidden, "服务器文件浏览已禁用，无法按路径安装")
		return
	}

	var req struct {
		Path string            `json:"path"`
		Set  map[string]string `json:"set"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.validatePackagePath(req.Path); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	task, err := s.startInstallTask(req.Path, "", req.Set)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": task.ID})
}

// handleInstallUpload POST /api/install/upload
// multipart/form-data：file=<fpk>、set[key]=value、mode=install|upgrade
func (s *Server) handleInstallUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	// 提前拦截过大请求
	if r.ContentLength > maxUploadBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("安装包过大（上限 %s）", humanSize(maxUploadBytes)))
		return
	}

	if err := r.ParseMultipartForm(uploadMemory); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析上传内容失败: "+err.Error())
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "缺少上传文件字段 file")
		return
	}
	defer file.Close()

	srcName := filepath.Base(header.Filename)
	if !strings.EqualFold(filepath.Ext(srcName), ".fpk") {
		writeJSONError(w, http.StatusBadRequest, "仅支持上传 .fpk 文件")
		return
	}

	// 落盘到临时目录，避免半截文件进入正式目录
	tmpDir := filepath.Join(s.cfg.BaseDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建临时目录失败: "+err.Error())
		return
	}
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("upload-%d-%s", time.Now().UnixNano(), srcName))

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建临时文件失败: "+err.Error())
		return
	}

	limited := io.LimitReader(file, maxUploadBytes+1)
	written, copyErr := io.Copy(dst, limited)
	closeErr := dst.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		writeJSONError(w, http.StatusInternalServerError, "写入上传文件失败: "+copyErr.Error())
		return
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		writeJSONError(w, http.StatusInternalServerError, "保存上传文件失败: "+closeErr.Error())
		return
	}
	if written > maxUploadBytes {
		os.Remove(tmpPath)
		writeJSONError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("安装包过大（上限 %s）", humanSize(maxUploadBytes)))
		return
	}

	// 校验确实是可解析的 fpk，避免脏文件进入安装流程
	info, err := fpk.InspectPackage(tmpPath, false)
	if err != nil {
		os.Remove(tmpPath)
		writeJSONError(w, http.StatusBadRequest, "不是有效的 .fpk 安装包: "+err.Error())
		return
	}

	// 归档到 packages 目录，便于后续复用/升级
	archiveDir := filepath.Join(s.cfg.AppsDir, "..", "packages")
	archiveDir = filepath.Clean(archiveDir)
	var keepPath = tmpPath
	if err := os.MkdirAll(archiveDir, 0755); err == nil {
		finalName := fmt.Sprintf("%s-%s.fpk", info.Appname, info.Version)
		finalPath := filepath.Join(archiveDir, finalName)
		if err := os.Rename(tmpPath, finalPath); err == nil {
			keepPath = finalPath
		} else {
			// 跨设备等情况下降级为拷贝
			if err := copyFileSimple(tmpPath, finalPath); err == nil {
				os.Remove(tmpPath)
				keepPath = finalPath
			}
		}
	}

	set := map[string]string{}
	if r.MultipartForm != nil {
		for k, v := range r.MultipartForm.Value {
			if strings.HasPrefix(k, "set[") && strings.HasSuffix(k, "]") {
				key := strings.TrimSuffix(strings.TrimPrefix(k, "set["), "]")
				if key != "" && len(v) > 0 {
					set[key] = v[0]
				}
			}
		}
	}

	task, err := s.startInstallTask(keepPath, header.Filename, set)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"taskId":  task.ID,
		"package": info,
	})
}

// startInstallTask 启动后台安装任务（安装/升级自动判定）
func (s *Server) startInstallTask(pkgPath, originalName string, set map[string]string) (*Task, error) {
	var setArgs []string
	for k, v := range set {
		setArgs = append(setArgs, k+"="+v)
	}

	title := "安装 " + filepath.Base(pkgPath)
	if originalName != "" {
		title = "安装 " + originalName
	}

	// 原子占用执行权：并发安装会创建/移动同一批目录，必须串行化
	task, ok := s.tasks.TryBegin("install", title)
	if !ok {
		return nil, fmt.Errorf("已有操作正在执行（%s），请稍后再试", task.Title)
	}

	go func() {
		defer s.tasks.Clear(task)

		// 判断是升级还是全新安装
		info, err := fpk.InspectPackage(pkgPath, false)
		if err != nil {
			task.append(LogLevel("error"), "解析安装包失败: "+err.Error())
			task.finish(TaskFailed, err.Error())
			return
		}

		// 安装前先把 manifest 声明的依赖应用装好。
		//
		// 缺依赖的应用通常启动即失败（找不到 python3 / node），
		// 用户看到的是"装好了却用不了"。飞牛原版应用中心会连带安装
		// 依赖，这里对齐该行为。升级时同样补齐，避免旧安装缺依赖。
		if deps := splitDepApps(info.InstallDepApps); len(deps) > 0 {
			task.append(LogLevel("info"),
				fmt.Sprintf("该应用依赖 %d 个程序，正在检查: %s", len(deps), strings.Join(deps, ", ")))
			s.ensureDependencies(task, deps, 0)
		}

		alreadyInstalled := false
		if _, statErr := os.Stat(s.cfg.AppManifestFile(info.Appname)); statErr == nil {
			alreadyInstalled = true
			task.append(LogLevel("info"), fmt.Sprintf("%s 已安装，执行升级流程", info.Appname))
			if err := s.mgr.Upgrade(pkgPath, setArgs); err != nil {
				task.append(LogLevel("error"), err.Error())
				task.finish(TaskFailed, err.Error())
				return
			}
		} else {
			if err := s.mgr.Install(pkgPath, setArgs); err != nil {
				task.append(LogLevel("error"), err.Error())
				task.finish(TaskFailed, err.Error())
				return
			}
		}

		// 安装/升级后自动启动。
		// 注意 Upgrade 内部会先停止应用，若不自动启动，升级完成后应用会
		// 停留在"已停止"，对用户来说很意外。
		if s.autoStart {
			if alreadyInstalled {
				task.append(LogLevel("info"), "升级完成，正在重新启动应用…")
			} else {
				task.append(LogLevel("info"), "安装完成，正在自动启动应用…")
			}
			if err := s.mgr.Start(info.Appname); err != nil {
				// 启动失败不回滚安装：包已经装好了，用户可在界面上重试启动
				msg := fmt.Sprintf("应用已安装，但自动启动失败: %v", err)
				task.append(LogLevel("warn"), msg)
				task.finish(TaskSuccess, msg)
				return
			}
			// 安装后自动启动同样属于"用户希望它运行"，
			// 记录后 FPKM 重启会自动恢复。
			if aerr := s.autostart.mark(info.Appname); aerr != nil {
				logger.Warn("记录应用自启动失败（不影响运行）: %v", aerr)
			}
			task.append(LogLevel("info"), "应用已启动")
		}

		task.finish(TaskSuccess, "")
	}()

	return task, nil
}

// ---------- 工具 ----------

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	i := -1
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func copyFileSimple(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
