package httpapi

// DIY 自定义牌面主题的 REST 接口。
//
// 设计要点:
//   - 读接口完全公开: 任何玩家选 DIY 主题都要能看到同一套牌面。
//   - 写接口(上传/删除/改密码)只凭一个管理密码, 没有账户体系。
//   - 图片以 base64 data URL 提交, 服务端嗅探真实类型后落盘, 文件名由服务端生成,
//     因此不存在路径穿越与"上传可执行文件"的风险。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"majiang-server/internal/game"
	"majiang-server/internal/secret"
	"majiang-server/internal/store"
)

// diyPasswordKey 数据库里保存密码哈希的键。
const diyPasswordKey = "password_hash"

// diyImageNameRe 服务端生成的图片文件名格式。
var diyImageNameRe = regexp.MustCompile(`^[a-f0-9]{16}\.(png|jpg|webp|gif)$`)

// diyImageMaxEdge 不做像素级校验, 只限制文件体积(由 cfg.DIY.MaxImageBytes 控制)。

// diyTileView 一张牌的自定义样式(对外)。
type diyTileView struct {
	Code      string `json:"code"`
	URL       string `json:"url"`
	Fit       string `json:"fit"`
	UpdatedAt int64  `json:"updatedAt"`
}

// diyStateView 全量 DIY 状态(对外, 公开)。
type diyStateView struct {
	Tiles       []diyTileView `json:"tiles"`
	PasswordSet bool          `json:"passwordSet"`
	Fits        []string      `json:"fits"`
	MaxBytes    int           `json:"maxBytes"`
}

// diyFits 可选的填充方式(与前端 object-fit 一致)。
var diyFits = []string{"cover", "contain", "fill", "scale-down", "none"}

// effectiveDIYPasswordHash 返回当前生效的密码哈希:
// 数据库有值用数据库, 否则退回配置里的初始密码(为空表示还没设过密码)。
func (r *Router) effectiveDIYPasswordHash(ctx context.Context) (string, error) {
	hash, err := r.store.DIYPasswordHash(ctx)
	if err != nil {
		return "", err
	}
	if hash != "" {
		return hash, nil
	}
	if r.cfg.DIY.Password == "" {
		return "", nil
	}
	// 首次启动时把配置里的初始密码固化到数据库, 之后设置页改密码不会与配置冲突。
	h, err := secret.HashPassword(r.cfg.DIY.Password)
	if err != nil {
		return "", err
	}
	if err := r.store.SetDIYPasswordHash(ctx, h); err != nil {
		return "", err
	}
	return h, nil
}

// checkDIYPassword 校验管理密码; 未设置密码时一律拒绝(必须先去设置页设密码)。
func (r *Router) checkDIYPassword(ctx context.Context, password string) (bool, error) {
	hash, err := r.effectiveDIYPasswordHash(ctx)
	if err != nil {
		return false, err
	}
	if hash == "" {
		return false, nil
	}
	return secret.VerifyPassword(hash, password), nil
}

// handleDIYState GET /api/v1/diy — 公开读取全部自定义牌面。
func (r *Router) handleDIYState(w http.ResponseWriter, req *http.Request) {
	rows, err := r.store.ListDIYTiles(req.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "读取 DIY 牌面失败")
		return
	}
	hash, err := r.effectiveDIYPasswordHash(req.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "读取 DIY 设置失败")
		return
	}
	tiles := make([]diyTileView, 0, len(rows))
	for _, row := range rows {
		tiles = append(tiles, diyTileView{
			Code: row.Code,
			URL:  "/api/v1/diy/images/" + row.FileName,
			Fit:  row.Fit,
			UpdatedAt: row.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, diyStateView{
		Tiles:       tiles,
		PasswordSet: hash != "",
		Fits:        diyFits,
		MaxBytes:    r.cfg.DIY.MaxImageBytes,
	})
}

// handleDIYAuth POST /api/v1/diy/auth — 校验管理密码(用于进入设置页)。
func (r *Router) handleDIYAuth(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	ok, err := r.checkDIYPassword(req.Context(), body.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "校验失败")
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "bad_password", "密码不正确")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDIYSetPassword POST /api/v1/diy/password — 首次设置或修改管理密码。
// 未设过密码时允许不带旧密码直接设置; 已设过则必须提供正确的旧密码。
func (r *Router) handleDIYSetPassword(w http.ResponseWriter, req *http.Request) {
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	if n := len(body.NewPassword); n < 4 || n > 64 {
		writeErr(w, http.StatusBadRequest, "bad_password", "新密码长度需在 4~64 个字符之间")
		return
	}
	hash, err := r.effectiveDIYPasswordHash(req.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "读取设置失败")
		return
	}
	if hash != "" && !secret.VerifyPassword(hash, body.CurrentPassword) {
		writeErr(w, http.StatusUnauthorized, "bad_password", "当前密码不正确")
		return
	}
	next, err := secret.HashPassword(body.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "保存密码失败")
		return
	}
	if err := r.store.SetDIYPasswordHash(req.Context(), next); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "保存密码失败")
		return
	}
	r.log.Info("DIY 管理密码已更新")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// decodeJSONBody 与 decodeJSON 相同, 但允许指定体积上限。
// 图片以 base64 放在 JSON 里, 约为原始字节的 4/3, 因此不能用默认的 1MB 上限。
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "body_too_large", "请求体过大")
		} else {
			writeErr(w, http.StatusBadRequest, "bad_json", "请求体格式错误: "+err.Error())
		}
		return false
	}
	return true
}

// handleDIYSaveTile POST /api/v1/diy/tiles — 上传/替换一张牌的自定义图片。
func (r *Router) handleDIYSaveTile(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
		Fit      string `json:"fit"`
		DataURL  string `json:"dataUrl"`
	}
	// base64 膨胀约 4/3, 再加 64KB 给其它字段留余量。
	limit := int64(r.cfg.DIY.MaxImageBytes)/3*4 + 64*1024
	if !decodeJSONBody(w, req, &body, limit) {
		return
	}
	if !r.requireDIYPassword(w, req.Context(), body.Password) {
		return
	}
	code := strings.ToLower(strings.TrimSpace(body.Code))
	if _, ok := game.KindFromCode(code); !ok {
		writeErr(w, http.StatusBadRequest, "bad_code", "未知的牌面标识")
		return
	}
	ext, raw, err := decodeImageDataURL(body.DataURL, r.cfg.DIY.MaxImageBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_image", err.Error())
		return
	}
	name := fmt.Sprintf("%016x.%s", time.Now().UnixNano(), ext)
	if err := r.writeDIYImage(name, raw); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "保存图片失败")
		return
	}
	old, err := r.store.SaveDIYTile(req.Context(), code, name, body.Fit)
	if err != nil {
		_ = r.removeDIYImage(name)
		writeErr(w, http.StatusInternalServerError, "internal", "保存样式失败")
		return
	}
	if old != "" && old != name {
		_ = r.removeDIYImage(old)
	}
	fit := store.NormalizeDIYFit(body.Fit)
	r.log.Info("DIY 牌面已更新", "code", code, "fit", fit)
	writeJSON(w, http.StatusOK, diyTileView{
		Code: code, URL: "/api/v1/diy/images/" + name, Fit: fit, UpdatedAt: time.Now().Unix(),
	})
}

// handleDIYDeleteTile POST /api/v1/diy/tiles/{code}/delete — 恢复某张牌的默认外观。
func (r *Router) handleDIYDeleteTile(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	if !r.requireDIYPassword(w, req.Context(), body.Password) {
		return
	}
	code := strings.ToLower(strings.TrimSpace(req.PathValue("code")))
	name, err := r.store.DeleteDIYTile(req.Context(), code)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "删除样式失败")
		return
	}
	if name != "" {
		_ = r.removeDIYImage(name)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDIYImage GET /api/v1/diy/images/{name} — 提供自定义牌面图片。
func (r *Router) handleDIYImage(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if !diyImageNameRe.MatchString(name) {
		http.NotFound(w, req)
		return
	}
	path := filepath.Join(r.cfg.DIY.Dir, name)
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", diyImageMime(name))
	// 文件名带纳秒时间戳, 内容不会变, 可以长缓存。
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, req, name, info.ModTime(), f)
}

// requireDIYPassword 校验密码, 失败时已写好响应并返回 false。
func (r *Router) requireDIYPassword(w http.ResponseWriter, ctx context.Context, password string) bool {
	ok, err := r.checkDIYPassword(ctx, password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "校验失败")
		return false
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "bad_password", "密码不正确")
		return false
	}
	return true
}

func (r *Router) writeDIYImage(name string, raw []byte) error {
	if err := os.MkdirAll(r.cfg.DIY.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.cfg.DIY.Dir, name), raw, 0o644)
}

func (r *Router) removeDIYImage(name string) error {
	if !diyImageNameRe.MatchString(name) {
		return nil
	}
	err := os.Remove(filepath.Join(r.cfg.DIY.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// decodeImageDataURL 解析 data:image/...;base64,... 并校验真实图片类型。
func decodeImageDataURL(dataURL string, maxBytes int) (string, []byte, error) {
	const prefix = "data:image/"
	if !strings.HasPrefix(dataURL, prefix) {
		return "", nil, errors.New("请上传图片文件")
	}
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		return "", nil, errors.New("图片数据格式不正确")
	}
	head := dataURL[len(prefix):comma]
	if !strings.HasSuffix(head, ";base64") {
		return "", nil, errors.New("图片数据格式不正确")
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[comma+1:])
	if err != nil {
		return "", nil, errors.New("图片数据损坏, 请重新选择")
	}
	if len(raw) > maxBytes {
		return "", nil, fmt.Errorf("图片不能超过 %d KB", maxBytes/1024)
	}
	ext, ok := sniffImageExt(raw)
	if !ok {
		return "", nil, errors.New("只支持 PNG / JPEG / WebP / GIF 图片")
	}
	return ext, raw, nil
}

// sniffImageExt 按文件头判断图片类型, 不信任客户端声明的 MIME。
func sniffImageExt(raw []byte) (string, bool) {
	switch {
	case len(raw) > 8 && string(raw[0:8]) == "\x89PNG\r\n\x1a\n":
		return "png", true
	case len(raw) > 3 && raw[0] == 0xFF && raw[1] == 0xD8 && raw[2] == 0xFF:
		return "jpg", true
	case len(raw) > 12 && string(raw[0:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		return "webp", true
	case len(raw) > 6 && (string(raw[0:6]) == "GIF87a" || string(raw[0:6]) == "GIF89a"):
		return "gif", true
	}
	return "", false
}

func diyImageMime(name string) string {
	switch filepath.Ext(name) {
	case ".png":
		return "image/png"
	case ".jpg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "application/octet-stream"
}
