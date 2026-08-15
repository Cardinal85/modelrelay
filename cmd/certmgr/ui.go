//go:build cgo

package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"modelrelay/internal/certmgr"
	"modelrelay/internal/version"
)

type uiState struct {
	win     fyne.Window
	agentCA *certmgr.Workspace
	relayCA *certmgr.Workspace
	admin   *certmgr.AdminClient

	agentInfo *widget.Label
	relayInfo *widget.Label
}

func runUI() {
	a := app.NewWithID("com.modelrelay.certmgr")
	applyCJKFont(a)
	w := a.NewWindow("ModelRelay 证书管理器")
	w.Resize(fyne.NewSize(980, 740))
	ui := &uiState{win: w}
	w.SetContent(ui.build())
	w.ShowAndRun()
}

func (ui *uiState) build() fyne.CanvasObject {
	banner := widget.NewLabel(fmt.Sprintf(
		"%s 证书管理器 %s    离线签发 · CA 私钥只留本机 · Agent 私钥必须在 GPU 主机生成",
		version.Name, version.ToolVersion(),
	))
	banner.Wrapping = fyne.TextWrapWord
	tabs := container.NewAppTabs(
		container.NewTabItem("CA 工作区", ui.tabCA()),
		container.NewTabItem("签发 Agent", ui.tabIssueAgent()),
		container.NewTabItem("签发 Relay", ui.tabIssueRelay()),
		container.NewTabItem("证书检查", ui.tabInspect()),
		container.NewTabItem("部署导出", ui.tabExport()),
		container.NewTabItem("在线吊销（可选）", ui.tabRevoke()),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return container.NewBorder(banner, nil, nil, nil, tabs)
}

func (ui *uiState) tabCA() fyne.CanvasObject {
	kind := widget.NewRadioGroup([]string{"Agent CA", "Relay CA"}, nil)
	kind.SetSelected("Agent CA")
	kind.Horizontal = true

	dir := widget.NewEntry()
	dir.SetText(filepath.Join(certmgr.DefaultWorkspaceRoot(), "agent"))
	kind.OnChanged = func(v string) {
		root := certmgr.DefaultWorkspaceRoot()
		if v == "Relay CA" {
			dir.SetText(filepath.Join(root, "relay"))
		} else {
			dir.SetText(filepath.Join(root, "agent"))
		}
	}

	cn := widget.NewEntry()
	cn.SetText(certmgr.DefaultCN(certmgr.KindAgent))
	days := widget.NewEntry()
	days.SetText(strconv.Itoa(certmgr.DefaultCADays))

	ui.agentInfo = widget.NewLabel("尚未打开 Agent CA")
	ui.agentInfo.Wrapping = fyne.TextWrapWord
	ui.relayInfo = widget.NewLabel("尚未打开 Relay CA")
	ui.relayInfo.Wrapping = fyne.TextWrapWord

	hint := widget.NewLabel("创建工作区后请离线备份 agent-ca.key / relay-ca.key。目录权限：Windows 使用 ACL，Linux/macOS 使用 0700/0600。")
	hint.Wrapping = fyne.TextWrapWord

	parseKind := func() certmgr.Kind {
		if kind.Selected == "Relay CA" {
			return certmgr.KindRelay
		}
		return certmgr.KindAgent
	}

	create := widget.NewButton("创建 CA 工作区", func() {
		k := parseKind()
		n, err := strconv.Atoi(strings.TrimSpace(days.Text))
		if err != nil || n <= 0 {
			ui.fail(fmt.Errorf("有效期必须是正整数天数"))
			return
		}
		ws, err := certmgr.CreateWorkspace(certmgr.CreateOptions{
			Dir:  strings.TrimSpace(dir.Text),
			Kind: k,
			CN:   strings.TrimSpace(cn.Text),
			Days: n,
		})
		if err != nil {
			ui.fail(err)
			return
		}
		ui.setWorkspace(ws)
		dialog.ShowInformation("已创建", ws.BackupHint(), ui.win)
	})
	open := widget.NewButton("打开 CA 工作区", func() {
		ws, err := certmgr.OpenWorkspace(strings.TrimSpace(dir.Text), parseKind())
		if err != nil {
			ui.fail(err)
			return
		}
		ui.setWorkspace(ws)
	})
	create.Importance = widget.HighImportance

	form := widget.NewForm(
		widget.NewFormItem("类型", kind),
		widget.NewFormItem("目录", pathRow(dir, func() { pickFolder(ui.win, dir) })),
		widget.NewFormItem("主题 CN", cn),
		widget.NewFormItem("有效期（天）", days),
	)
	return padded(container.NewVBox(
		form,
		container.NewHBox(create, open),
		hint,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("当前 Agent CA", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		ui.agentInfo,
		widget.NewLabelWithStyle("当前 Relay CA", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		ui.relayInfo,
	))
}

func (ui *uiState) tabIssueAgent() fyne.CanvasObject {
	csrPath := widget.NewEntry()
	nodeID := widget.NewEntry()
	days := widget.NewEntry()
	days.SetText(strconv.Itoa(certmgr.DefaultIssueDays))
	outDir := widget.NewEntry()
	meta := widget.NewMultiLineEntry()
	meta.SetPlaceHolder("导入 CSR 后将显示 CN / URI SAN 校验结果")
	meta.Wrapping = fyne.TextWrapWord
	result := widget.NewMultiLineEntry()
	result.Wrapping = fyne.TextWrapWord

	load := func() {
		info, _, err := certmgr.ReadCSR(strings.TrimSpace(csrPath.Text), strings.TrimSpace(nodeID.Text))
		if err != nil {
			ui.fail(err)
			return
		}
		nodeID.SetText(info.NodeID)
		meta.SetText(fmt.Sprintf("CN: %s\nnode_id: %s\nURI SAN: %s\n校验通过，可以签发。",
			info.CommonName, info.NodeID, strings.Join(info.URIs, ", ")))
	}

	issue := widget.NewButton("签发 Agent 证书", func() {
		if ui.agentCA == nil {
			ui.fail(fmt.Errorf("请先打开 Agent CA 工作区"))
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(days.Text))
		if err != nil || n <= 0 {
			ui.fail(fmt.Errorf("有效期必须是正整数天数"))
			return
		}
		res, err := ui.agentCA.IssueAgent(certmgr.IssueAgentOptions{
			CSRPath: strings.TrimSpace(csrPath.Text),
			NodeID:  strings.TrimSpace(nodeID.Text),
			Days:    n,
			OutDir:  strings.TrimSpace(outDir.Text),
		})
		if err != nil {
			ui.fail(err)
			return
		}
		msg := res.Info.Text()
		if res.Path != "" {
			msg += "\n写入: " + res.Path
		}
		result.SetText(msg)
		if res.Info.Warning != "" {
			dialog.ShowInformation("签发完成（有提示）", res.Info.Warning+"\n\n"+msg, ui.win)
			return
		}
		dialog.ShowInformation("签发完成", "已签发 "+res.NodeID+"，Agent 私钥仍必须留在 GPU 主机。", ui.win)
	})
	issue.Importance = widget.HighImportance

	form := widget.NewForm(
		widget.NewFormItem("CSR 文件", pathRow(csrPath, func() {
			pickFile(ui.win, []string{".csr", ".pem"}, csrPath)
		})),
		widget.NewFormItem("node_id", nodeID),
		widget.NewFormItem("有效期（天）", days),
		widget.NewFormItem("输出目录", pathRow(outDir, func() { pickFolder(ui.win, outDir) })),
	)
	return padded(container.NewVBox(
		widget.NewLabel("只导入 GPU 主机生成的 CSR。本程序不会生成或保存 Agent 私钥。"),
		form,
		container.NewHBox(widget.NewButton("校验 CSR", load), issue),
		widget.NewLabelWithStyle("CSR 信息", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		meta,
		widget.NewLabelWithStyle("签发结果", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		result,
	))
}

func (ui *uiState) tabIssueRelay() fyne.CanvasObject {
	cn := widget.NewEntry()
	cn.SetPlaceHolder("relay.example.com")
	dns := widget.NewEntry()
	dns.SetPlaceHolder("relay.example.com, relay.internal")
	ips := widget.NewEntry()
	ips.SetPlaceHolder("203.0.113.10")
	days := widget.NewEntry()
	days.SetText(strconv.Itoa(certmgr.DefaultIssueDays))
	outDir := widget.NewEntry()
	result := widget.NewMultiLineEntry()
	result.Wrapping = fyne.TextWrapWord

	issue := widget.NewButton("签发 Relay 服务端证书", func() {
		if ui.relayCA == nil {
			ui.fail(fmt.Errorf("请先打开 Relay CA 工作区"))
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(days.Text))
		if err != nil || n <= 0 {
			ui.fail(fmt.Errorf("有效期必须是正整数天数"))
			return
		}
		res, err := ui.relayCA.IssueRelay(certmgr.IssueRelayOptions{
			CN:       strings.TrimSpace(cn.Text),
			DNSNames: splitCSV(dns.Text),
			IPs:      splitCSV(ips.Text),
			Days:     n,
			OutDir:   strings.TrimSpace(outDir.Text),
		})
		if err != nil {
			ui.fail(err)
			return
		}
		msg := res.Info.Text()
		if res.CertPath != "" {
			msg += "\n证书: " + res.CertPath + "\n私钥: " + res.KeyPath
		}
		result.SetText(msg)
		dialog.ShowInformation("签发完成", "已生成 Relay 服务端证书和私钥。", ui.win)
	})
	issue.Importance = widget.HighImportance

	form := widget.NewForm(
		widget.NewFormItem("CN", cn),
		widget.NewFormItem("DNS SAN", dns),
		widget.NewFormItem("IP SAN", ips),
		widget.NewFormItem("有效期（天）", days),
		widget.NewFormItem("输出目录", pathRow(outDir, func() { pickFolder(ui.win, outDir) })),
	)
	return padded(container.NewVBox(
		widget.NewLabel("DNS/IP SAN 必须包含 Agent 实际连接 Relay 时使用的地址。"),
		form,
		issue,
		widget.NewLabelWithStyle("签发结果", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		result,
	))
}

func (ui *uiState) tabInspect() fyne.CanvasObject {
	path := widget.NewEntry()
	out := widget.NewMultiLineEntry()
	out.Wrapping = fyne.TextWrapWord
	out.SetMinRowsVisible(18)

	run := widget.NewButton("检查证书", func() {
		info, err := certmgr.InspectFile(strings.TrimSpace(path.Text))
		if err != nil {
			ui.fail(err)
			return
		}
		out.SetText(info.Text())
		if info.Warning != "" {
			dialog.ShowInformation("证书提示", info.Warning, ui.win)
		}
	})
	run.Importance = widget.HighImportance
	form := widget.NewForm(
		widget.NewFormItem("证书文件", pathRow(path, func() {
			pickFile(ui.win, []string{".crt", ".pem", ".cer"}, path)
		})),
	)
	return padded(container.NewVBox(form, run, out))
}

func (ui *uiState) tabExport() fyne.CanvasObject {
	relayCert := widget.NewEntry()
	relayKey := widget.NewEntry()
	agentCA := widget.NewEntry()
	relayDest := widget.NewEntry()

	agentCert := widget.NewEntry()
	relayCA := widget.NewEntry()
	agentDest := widget.NewEntry()
	nodeID := widget.NewEntry()

	warn := widget.NewLabel("Agent 私钥必须由 Agent 主机本地产生，证书管理器不会生成、也不会把私钥导出到部署目录。CA 私钥不会被导出。")
	warn.Wrapping = fyne.TextWrapWord

	exportRelay := widget.NewButton("导出 Relay 文件", func() {
		if strings.TrimSpace(agentCA.Text) == "" && ui.agentCA != nil {
			agentCA.SetText(ui.agentCA.CertPath())
		}
		err := certmgr.ExportRelay(certmgr.RelayExportInput{
			ServerCertPath: strings.TrimSpace(relayCert.Text),
			ServerKeyPath:  strings.TrimSpace(relayKey.Text),
			AgentCAPath:    strings.TrimSpace(agentCA.Text),
			DestDir:        strings.TrimSpace(relayDest.Text),
		})
		if err != nil {
			ui.fail(err)
			return
		}
		dialog.ShowInformation("已导出", "Relay 部署文件已写入：\n"+strings.TrimSpace(relayDest.Text), ui.win)
	})
	exportAgent := widget.NewButton("导出 Agent 文件", func() {
		if strings.TrimSpace(relayCA.Text) == "" && ui.relayCA != nil {
			relayCA.SetText(ui.relayCA.CertPath())
		}
		err := certmgr.ExportAgent(certmgr.AgentExportInput{
			AgentCertPath: strings.TrimSpace(agentCert.Text),
			RelayCAPath:   strings.TrimSpace(relayCA.Text),
			DestDir:       strings.TrimSpace(agentDest.Text),
			NodeID:        strings.TrimSpace(nodeID.Text),
		})
		if err != nil {
			ui.fail(err)
			return
		}
		dialog.ShowInformation("已导出", "Agent 部署文件已写入（不含私钥）：\n"+strings.TrimSpace(agentDest.Text), ui.win)
	})

	relayBox := widget.NewCard("Relay 主机", "复制 relay.crt / relay.key / agent-ca.crt", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("服务端证书", pathRow(relayCert, func() { pickFile(ui.win, []string{".crt", ".pem"}, relayCert) })),
			widget.NewFormItem("服务端私钥", pathRow(relayKey, func() { pickFile(ui.win, []string{".key", ".pem"}, relayKey) })),
			widget.NewFormItem("Agent CA 公钥", pathRow(agentCA, func() { pickFile(ui.win, []string{".crt", ".pem"}, agentCA) })),
			widget.NewFormItem("导出目录", pathRow(relayDest, func() { pickFolder(ui.win, relayDest) })),
		),
		exportRelay,
	))
	agentBox := widget.NewCard("Agent 主机", "只复制证书和 Relay CA 公钥，不复制私钥", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Agent 证书", pathRow(agentCert, func() { pickFile(ui.win, []string{".crt", ".pem"}, agentCert) })),
			widget.NewFormItem("Relay CA 公钥", pathRow(relayCA, func() { pickFile(ui.win, []string{".crt", ".pem"}, relayCA) })),
			widget.NewFormItem("node_id", nodeID),
			widget.NewFormItem("导出目录", pathRow(agentDest, func() { pickFolder(ui.win, agentDest) })),
		),
		exportAgent,
	))
	return padded(container.NewVBox(warn, relayBox, agentBox))
}

func (ui *uiState) tabRevoke() fyne.CanvasObject {
	base := widget.NewEntry()
	base.SetPlaceHolder("http://127.0.0.1:9200")
	user := widget.NewEntry()
	user.SetText("admin")
	pass := widget.NewPasswordEntry()
	status := widget.NewLabel("未登录。吊销才需要连接 Relay；密码不会保存。")
	status.Wrapping = fyne.TextWrapWord
	list := widget.NewMultiLineEntry()
	list.Wrapping = fyne.TextWrapWord
	list.SetMinRowsVisible(12)
	serial := widget.NewEntry()
	serial.SetPlaceHolder("要吊销的证书序列号")

	login := widget.NewButton("登录", func() {
		c, err := certmgr.NewAdminClient(strings.TrimSpace(base.Text))
		if err != nil {
			ui.fail(err)
			return
		}
		if err := c.Login(strings.TrimSpace(user.Text), pass.Text); err != nil {
			pass.SetText("")
			ui.fail(err)
			return
		}
		pass.SetText("")
		ui.admin = c
		status.SetText("已登录: " + c.Username() + "（会话 Cookie，密码未保存）")
		certs, err := c.ListCerts()
		if err != nil {
			ui.fail(err)
			return
		}
		list.SetText(formatRemoteCerts(certs))
	})
	refresh := widget.NewButton("刷新列表", func() {
		if ui.admin == nil || !ui.admin.LoggedIn() {
			ui.fail(fmt.Errorf("请先登录 Relay 管理 API"))
			return
		}
		certs, err := ui.admin.ListCerts()
		if err != nil {
			ui.fail(err)
			return
		}
		list.SetText(formatRemoteCerts(certs))
	})
	revoke := widget.NewButton("吊销", func() {
		if ui.admin == nil || !ui.admin.LoggedIn() {
			ui.fail(fmt.Errorf("请先登录 Relay 管理 API"))
			return
		}
		s := strings.TrimSpace(serial.Text)
		dialog.ShowConfirm("确认吊销", "吊销后 Relay 会立即断开并拒绝该证书重连。\n序列号: "+s, func(ok bool) {
			if !ok {
				return
			}
			if err := ui.admin.Revoke(s); err != nil {
				ui.fail(err)
				return
			}
			dialog.ShowInformation("已吊销", "序列号 "+s+" 已在 Relay 生效。", ui.win)
			refresh.OnTapped()
		}, ui.win)
	})
	revoke.Importance = widget.DangerImportance

	form := widget.NewForm(
		widget.NewFormItem("管理 API", base),
		widget.NewFormItem("用户名", user),
		widget.NewFormItem("密码", pass),
	)
	return padded(container.NewVBox(
		widget.NewLabel("证书管理机无需常在线。只有签发/轮换时使用本程序；只有吊销时才连接 Relay。"),
		form,
		container.NewHBox(login, refresh),
		status,
		list,
		widget.NewForm(widget.NewFormItem("序列号", serial)),
		revoke,
	))
}

func (ui *uiState) setWorkspace(ws *certmgr.Workspace) {
	text := fmt.Sprintf("目录: %s\n%s\n%s", ws.Dir, strings.TrimSpace(ws.Info.Text()), ws.BackupHint())
	if ws.Kind == certmgr.KindRelay {
		ui.relayCA = ws
		ui.relayInfo.SetText(text)
		return
	}
	ui.agentCA = ws
	ui.agentInfo.SetText(text)
}

func (ui *uiState) fail(err error) {
	dialog.ShowError(err, ui.win)
}

func pathRow(entry *widget.Entry, browse func()) fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, widget.NewButton("浏览", browse), entry)
}

func pickFolder(win fyne.Window, dest *widget.Entry) {
	dialog.ShowFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		if lu == nil {
			return
		}
		dest.SetText(uriToPath(lu))
	}, win)
}

func pickFile(win fyne.Window, exts []string, dest *widget.Entry) {
	d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		if rc == nil {
			return
		}
		defer rc.Close()
		dest.SetText(uriToPath(rc.URI()))
	}, win)
	if len(exts) > 0 {
		d.SetFilter(storage.NewExtensionFileFilter(exts))
	}
	d.Show()
}

func uriToPath(u fyne.URI) string {
	if u == nil {
		return ""
	}
	p := u.Path()
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.Clean(filepath.FromSlash(p))
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatRemoteCerts(certs []certmgr.RemoteCert) string {
	if len(certs) == 0 {
		return "（没有证书记录）"
	}
	var b strings.Builder
	for _, c := range certs {
		fmt.Fprintf(&b, "%s  %s  %s  %s  截止 %s\n",
			c.Serial, c.NodeID, c.Status, c.Subject, c.NotAfter.Format("2006-01-02"))
	}
	return b.String()
}

func padded(obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewPadded(obj)
}

type cjkTheme struct {
	base fyne.Theme
	font fyne.Resource
}

func (t *cjkTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return t.base.Color(n, v)
}
func (t *cjkTheme) Font(style fyne.TextStyle) fyne.Resource {
	if t.font != nil {
		return t.font
	}
	return t.base.Font(style)
}
func (t *cjkTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.base.Icon(n) }
func (t *cjkTheme) Size(n fyne.ThemeSizeName) float32       { return t.base.Size(n) }

func applyCJKFont(a fyne.App) {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		fonts := filepath.Join(windir, "Fonts")
		candidates = []string{
			filepath.Join(fonts, "Noto Sans SC.ttf"),
			filepath.Join(fonts, "simhei.ttf"),
			filepath.Join(fonts, "Deng.ttf"),
			filepath.Join(fonts, "simkai.ttf"),
			filepath.Join(fonts, "msyh.ttf"),
		}
	case "darwin":
		candidates = []string{
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
		}
	default:
		candidates = []string{
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
			"/usr/share/fonts/truetype/arphic/uming.ttc",
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		}
	}
	for _, p := range candidates {
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".ttc" || ext == ".otc" {
			// Fyne's bundled font mapper rejects TrueType collections.
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		res, err := fyne.LoadResourceFromPath(p)
		if err != nil {
			continue
		}
		a.Settings().SetTheme(&cjkTheme{base: theme.DefaultTheme(), font: res})
		return
	}
}
