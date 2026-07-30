// UniDrop's Linux shell implements the Freedesktop StatusNotifierItem protocol.
// It deliberately stays small: the secure transfer core owns all file/network
// state, while this process owns the desktop tray icon and menu.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

var appVersion = "0.3.2"

const (
	defaultUIURL = "http://127.0.0.1:43337/"

	statusItemInterface      = "org.kde.StatusNotifierItem"
	freedesktopItemInterface = "org.freedesktop.StatusNotifierItem"
	menuInterface            = "com.canonical.dbusmenu"
	statusItemPath           = dbus.ObjectPath("/StatusNotifierItem")
	menuPath                 = dbus.ObjectPath("/Menu")

	menuStatus int32 = iota + 1
	menuOpen
	menuSeparatorOne
	menuModeAsk
	menuModeTrusted
	menuModeOff
	menuSeparatorTwo
	menuDownloads
	menuSeparatorThree
	menuQuit
)

type coreSummary struct {
	Nearby         int    `json:"nearby"`
	Pending        int    `json:"pending"`
	ReceiveMode    string `json:"receive_mode"`
	DiscoveryState string `json:"discovery_status"`
	Available      bool   `json:"-"`
}

type iconPixmap struct {
	Width  int32
	Height int32
	Data   []byte
}

type toolTip struct {
	IconName   string
	IconPixmap []iconPixmap
	Title      string
	Text       string
}

type menuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

type menuPropertyGroup struct {
	ID         int32
	Properties map[string]dbus.Variant
}

type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

type coreClient struct {
	baseURL   string
	configDir string
	http      *http.Client
}

func newCoreClient(baseURL string) (*coreClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, errors.New("the UniDrop tray UI must use an HTTP loopback address")
	}
	loopback := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1"
	cleanPath := parsed.Path == "" || parsed.Path == "/"
	if parsed.Scheme != "http" || !loopback || !cleanPath || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("the UniDrop tray UI must use an HTTP loopback address")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find config directory: %w", err)
	}
	configDir = filepath.Join(configDir, "UniDrop")
	if override := strings.TrimSpace(os.Getenv("UNIDROP_CONFIG_DIR")); override != "" {
		configDir = override
	}
	return &coreClient{
		baseURL:   strings.TrimRight(baseURL, "/") + "/",
		configDir: configDir,
		http:      &http.Client{Timeout: 2 * time.Second},
	}, nil
}

func (c *coreClient) summary() (coreSummary, error) {
	var summary coreSummary
	request, err := http.NewRequest(http.MethodGet, c.baseURL+"api/summary", nil)
	if err != nil {
		return summary, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return summary, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return summary, responseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&summary); err != nil {
		return summary, err
	}
	summary.Available = true
	return summary, nil
}

func (c *coreClient) setReceiveMode(mode string) error {
	if mode != "ask" && mode != "trusted" && mode != "off" {
		return errors.New("invalid receive mode")
	}
	return c.post("api/cli/receive-mode", map[string]string{"mode": mode})
}

func (c *coreClient) openDownloads() error {
	return c.post("api/cli/open-downloads", nil)
}

func (c *coreClient) shutdown() error {
	return c.post("api/cli/shutdown", nil)
}

func (c *coreClient) post(path string, body any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	token, err := c.controlToken()
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, c.baseURL+strings.TrimLeft(path, "/"), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	return nil
}

func (c *coreClient) controlToken() (string, error) {
	data, err := os.ReadFile(filepath.Join(c.configDir, "control-token"))
	if err != nil {
		return "", fmt.Errorf("read UniDrop control token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) != 64 {
		return "", errors.New("invalid UniDrop control token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("invalid UniDrop control token")
	}
	return token, nil
}

func responseError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &payload)
	if strings.TrimSpace(payload.Error) != "" {
		return errors.New(payload.Error)
	}
	return fmt.Errorf("UniDrop returned %s", response.Status)
}

type trayApp struct {
	conn            *dbus.Conn
	client          *coreClient
	itemProps       *prop.Properties
	menuProps       *prop.Properties
	traditionalName string
	freedesktopName string
	watcherOwners   map[string]string

	mu       sync.RWMutex
	summary  coreSummary
	revision uint32
	done     chan struct{}
	doneOnce sync.Once
}

type statusNotifier struct{ app *trayApp }

func (s *statusNotifier) Activate(_, _ int32) *dbus.Error {
	s.app.openPanel()
	return nil
}

func (s *statusNotifier) SecondaryActivate(_, _ int32) *dbus.Error {
	s.app.openPanel()
	return nil
}

func (s *statusNotifier) ContextMenu(_, _ int32) *dbus.Error   { return nil }
func (s *statusNotifier) Scroll(_ int32, _ string) *dbus.Error { return nil }

type trayMenu struct{ app *trayApp }

func (m *trayMenu) GetLayout(parentID, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	revision, layout, err := m.app.menuLayout(parentID, recursionDepth, propertyNames)
	if err != nil {
		return revision, menuLayout{}, dbus.MakeFailedError(err)
	}
	return revision, layout, nil
}

func (m *trayMenu) GetGroupProperties(ids []int32, propertyNames []string) ([]menuPropertyGroup, *dbus.Error) {
	return m.app.menuGroupProperties(ids, propertyNames), nil
}

func (m *trayMenu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	properties := m.app.menuProperties(id, []string{name})
	value, ok := properties[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("com.canonical.dbusmenu.Error.UnknownProperty", []any{name})
	}
	return value, nil
}

func (m *trayMenu) Event(id int32, eventID string, _ dbus.Variant, _ uint32) *dbus.Error {
	if eventID == "clicked" {
		go m.app.dispatch(id)
	}
	return nil
}

func (m *trayMenu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	invalid := make([]int32, 0)
	for _, event := range events {
		if event.EventID != "clicked" || !m.app.validMenuID(event.ID) {
			invalid = append(invalid, event.ID)
			continue
		}
		go m.app.dispatch(event.ID)
	}
	return invalid, nil
}

func (m *trayMenu) AboutToShow(_ int32) (bool, *dbus.Error) { return false, nil }

func (m *trayMenu) AboutToShowGroup(_ []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}

func (a *trayApp) validMenuID(id int32) bool {
	return id == menuOpen || id == menuModeAsk || id == menuModeTrusted || id == menuModeOff || id == menuDownloads || id == menuQuit
}

func (a *trayApp) dispatch(id int32) {
	var err error
	switch id {
	case menuOpen:
		a.openPanel()
		return
	case menuModeAsk:
		err = a.client.setReceiveMode("ask")
	case menuModeTrusted:
		err = a.client.setReceiveMode("trusted")
	case menuModeOff:
		err = a.client.setReceiveMode("off")
	case menuDownloads:
		err = a.client.openDownloads()
	case menuQuit:
		err = a.client.shutdown()
	}
	if err != nil {
		log.Printf("tray action failed: %v", err)
		a.notify("UniDrop action failed", err.Error())
		return
	}
	if id == menuQuit {
		a.doneOnce.Do(func() { close(a.done) })
		return
	}
	if id == menuModeAsk || id == menuModeTrusted || id == menuModeOff {
		a.refreshSummary()
	}
}

func (a *trayApp) openPanel() {
	if err := exec.Command("xdg-open", a.client.baseURL).Start(); err != nil {
		log.Printf("open control panel: %v", err)
	}
}

func (a *trayApp) notify(title, message string) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	_ = exec.Command("notify-send", "--app-name=UniDrop", "--icon=unidrop", title, message).Start()
}

func (a *trayApp) snapshot() (coreSummary, uint32) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.summary, a.revision
}

func (a *trayApp) menuItems() []int32 {
	return []int32{menuStatus, menuOpen, menuSeparatorOne, menuModeAsk, menuModeTrusted, menuModeOff, menuSeparatorTwo, menuDownloads, menuSeparatorThree, menuQuit}
}

func (a *trayApp) menuProperties(id int32, requested []string) map[string]dbus.Variant {
	summary, _ := a.snapshot()
	properties := map[string]dbus.Variant{}
	switch id {
	case 0:
		properties["children-display"] = dbus.MakeVariant("submenu")
	case menuStatus:
		properties["label"] = dbus.MakeVariant(summaryLabel(summary))
		properties["enabled"] = dbus.MakeVariant(false)
	case menuOpen:
		label := "Open UniDrop"
		if summary.Pending > 0 {
			label = fmt.Sprintf("Open UniDrop — %d waiting", summary.Pending)
		}
		properties["label"] = dbus.MakeVariant(label)
	case menuSeparatorOne, menuSeparatorTwo, menuSeparatorThree:
		properties["type"] = dbus.MakeVariant("separator")
	case menuModeAsk:
		properties = radioProperties("Ask before receiving", summary.ReceiveMode == "ask")
	case menuModeTrusted:
		properties = radioProperties("Auto-accept paired devices", summary.ReceiveMode == "trusted")
	case menuModeOff:
		properties = radioProperties("Receiving paused", summary.ReceiveMode == "off")
	case menuDownloads:
		properties["label"] = dbus.MakeVariant("Open received files")
	case menuQuit:
		properties["label"] = dbus.MakeVariant("Quit UniDrop")
	default:
		return map[string]dbus.Variant{}
	}
	return filterProperties(properties, requested)
}

func radioProperties(label string, checked bool) map[string]dbus.Variant {
	state := int32(0)
	if checked {
		state = 1
	}
	return map[string]dbus.Variant{
		"label":        dbus.MakeVariant(label),
		"toggle-type":  dbus.MakeVariant("radio"),
		"toggle-state": dbus.MakeVariant(state),
	}
}

func filterProperties(properties map[string]dbus.Variant, requested []string) map[string]dbus.Variant {
	if len(requested) == 0 {
		return properties
	}
	wanted := make(map[string]bool, len(requested))
	for _, name := range requested {
		wanted[name] = true
	}
	filtered := make(map[string]dbus.Variant, len(requested))
	for name, value := range properties {
		if wanted[name] {
			filtered[name] = value
		}
	}
	return filtered
}

func (a *trayApp) menuLayout(parentID, recursionDepth int32, requested []string) (uint32, menuLayout, error) {
	_, revision := a.snapshot()
	if parentID != 0 && !containsID(a.menuItems(), parentID) {
		return revision, menuLayout{}, fmt.Errorf("unknown menu item %d", parentID)
	}
	layout := menuLayout{ID: parentID, Properties: a.menuProperties(parentID, requested), Children: []dbus.Variant{}}
	if parentID == 0 && recursionDepth != 0 {
		for _, id := range a.menuItems() {
			child := menuLayout{ID: id, Properties: a.menuProperties(id, requested), Children: []dbus.Variant{}}
			layout.Children = append(layout.Children, dbus.MakeVariant(child))
		}
	}
	return revision, layout, nil
}

func (a *trayApp) menuGroupProperties(ids []int32, requested []string) []menuPropertyGroup {
	if len(ids) == 0 {
		ids = append([]int32{0}, a.menuItems()...)
	}
	result := make([]menuPropertyGroup, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !containsID(a.menuItems(), id) {
			continue
		}
		result = append(result, menuPropertyGroup{ID: id, Properties: a.menuProperties(id, requested)})
	}
	return result
}

func containsID(ids []int32, target int32) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func summaryLabel(summary coreSummary) string {
	if !summary.Available {
		return "UniDrop • reconnecting"
	}
	return fmt.Sprintf("UniDrop • %d nearby • %d waiting", summary.Nearby, summary.Pending)
}

func (a *trayApp) refreshSummary() {
	next, err := a.client.summary()
	if err != nil {
		next = coreSummary{Available: false}
	}
	a.mu.Lock()
	previous := a.summary
	changed := previous != next
	if changed {
		a.summary = next
		a.revision++
	}
	revision := a.revision
	a.mu.Unlock()
	if !changed || a.itemProps == nil {
		return
	}

	title, detail, status, iconState := statusPresentation(next)
	pixmaps := makeIconSet(iconState)
	for _, itemInterface := range []string{statusItemInterface, freedesktopItemInterface} {
		a.itemProps.SetMust(itemInterface, "Title", title)
		a.itemProps.SetMust(itemInterface, "Status", status)
		a.itemProps.SetMust(itemInterface, "IconPixmap", pixmaps)
		a.itemProps.SetMust(itemInterface, "AttentionIconPixmap", makeIconSet(iconAttention))
		a.itemProps.SetMust(itemInterface, "ToolTip", toolTip{IconPixmap: pixmaps, Title: title, Text: detail})
		_ = a.conn.Emit(statusItemPath, itemInterface+".NewTitle")
		_ = a.conn.Emit(statusItemPath, itemInterface+".NewStatus", status)
		_ = a.conn.Emit(statusItemPath, itemInterface+".NewIcon")
		_ = a.conn.Emit(statusItemPath, itemInterface+".NewToolTip")
	}
	_ = a.conn.Emit(menuPath, menuInterface+".LayoutUpdated", revision, int32(0))
	if next.Pending > previous.Pending && next.Pending > 0 {
		a.notify("Incoming UniDrop request", fmt.Sprintf("%d file request waiting for approval", next.Pending))
	}
}

type iconState int

const (
	iconOffline iconState = iota
	iconNormal
	iconNearby
	iconAttention
)

func statusPresentation(summary coreSummary) (title, detail, status string, state iconState) {
	if !summary.Available {
		return "UniDrop", "Secure service is reconnecting", "Active", iconOffline
	}
	if summary.Pending > 0 {
		return "UniDrop", fmt.Sprintf("%d incoming request waiting", summary.Pending), "NeedsAttention", iconAttention
	}
	if summary.Nearby > 0 {
		return "UniDrop", fmt.Sprintf("%d nearby device available", summary.Nearby), "Active", iconNearby
	}
	detail = "Searching automatically for nearby devices"
	if summary.ReceiveMode == "off" {
		detail = "Receiving is paused"
	}
	return "UniDrop", detail, "Active", iconNormal
}

func makeIconSet(state iconState) []iconPixmap {
	return []iconPixmap{makeIcon(22, state), makeIcon(32, state), makeIcon(48, state)}
}

func makeIcon(size int, state iconState) iconPixmap {
	data := make([]byte, size*size*4)
	center := float64(size-1) / 2
	radius := float64(size) * 0.46
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-center, float64(y)-center
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			r, g, b := byte(75), byte(92), byte(255)
			switch state {
			case iconOffline:
				r, g, b = 105, 110, 124
			case iconNearby:
				r, g, b = 50, 184, 142
			case iconAttention:
				r, g, b = 244, 91, 112
			}
			shade := byte((y * 25) / size)
			setPixel(data, size, x, y, 255, r, clampByte(int(g)+int(shade)), clampByte(int(b)+int(shade)/2))
		}
	}
	white := [4]byte{255, 255, 255, 255}
	stroke := max(1, size/16)
	drawVerticalArrow(data, size, size*7/20, size*5/20, size*14/20, true, stroke, white)
	drawVerticalArrow(data, size, size*13/20, size*15/20, size*6/20, false, stroke, white)
	if state == iconAttention {
		dotRadius := max(1, size/9)
		for y := size - dotRadius*2 - 1; y < size-1; y++ {
			for x := size - dotRadius*2 - 1; x < size-1; x++ {
				dx, dy := x-(size-dotRadius-1), y-(size-dotRadius-1)
				if dx*dx+dy*dy <= dotRadius*dotRadius {
					setPixel(data, size, x, y, 255, 255, 218, 84)
				}
			}
		}
	}
	return iconPixmap{Width: int32(size), Height: int32(size), Data: data}
}

func drawVerticalArrow(data []byte, size, x, start, end int, down bool, stroke int, color [4]byte) {
	low, high := start, end
	if low > high {
		low, high = high, low
	}
	for y := low; y <= high; y++ {
		for offset := -stroke; offset <= stroke; offset++ {
			setPixel(data, size, x+offset, y, color[0], color[1], color[2], color[3])
		}
	}
	tipY, direction := end, -1
	if !down {
		direction = 1
	}
	wing := max(2, size/7)
	for step := 0; step <= wing; step++ {
		for offset := -stroke; offset <= stroke; offset++ {
			setPixel(data, size, x-step, tipY+direction*step+offset, color[0], color[1], color[2], color[3])
			setPixel(data, size, x+step, tipY+direction*step+offset, color[0], color[1], color[2], color[3])
		}
	}
}

func setPixel(data []byte, size, x, y int, alpha, red, green, blue byte) {
	if x < 0 || y < 0 || x >= size || y >= size {
		return
	}
	offset := (y*size + x) * 4
	data[offset], data[offset+1], data[offset+2], data[offset+3] = alpha, red, green, blue
}

func clampByte(value int) byte {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return byte(value)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *trayApp) export() error {
	initialPixmaps := makeIconSet(iconOffline)
	itemMap := prop.Map{
		statusItemInterface:      statusItemPropertyMap(initialPixmaps),
		freedesktopItemInterface: statusItemPropertyMap(initialPixmaps),
	}
	var err error
	a.itemProps, err = prop.Export(a.conn, statusItemPath, itemMap)
	if err != nil {
		return err
	}
	item := &statusNotifier{app: a}
	if err := a.conn.Export(item, statusItemPath, statusItemInterface); err != nil {
		return err
	}
	if err := a.conn.Export(item, statusItemPath, freedesktopItemInterface); err != nil {
		return err
	}
	itemNode := &introspect.Node{Name: string(statusItemPath), Interfaces: []introspect.Interface{
		prop.IntrospectData,
		{
			Name:       statusItemInterface,
			Methods:    introspect.Methods(item),
			Properties: a.itemProps.Introspection(statusItemInterface),
			Signals: []introspect.Signal{
				{Name: "NewTitle"}, {Name: "NewIcon"}, {Name: "NewAttentionIcon"},
				{Name: "NewOverlayIcon"}, {Name: "NewToolTip"},
				{Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}},
			},
		},
		{
			Name:       freedesktopItemInterface,
			Methods:    introspect.Methods(item),
			Properties: a.itemProps.Introspection(freedesktopItemInterface),
			Signals: []introspect.Signal{
				{Name: "NewTitle"}, {Name: "NewIcon"}, {Name: "NewAttentionIcon"},
				{Name: "NewOverlayIcon"}, {Name: "NewToolTip"},
				{Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}},
			},
		},
	}}
	if err := a.conn.Export(introspect.NewIntrospectable(itemNode), statusItemPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}

	menuMap := prop.Map{menuInterface: {
		"Version":       {Value: uint32(3), Emit: prop.EmitConst},
		"TextDirection": {Value: "ltr", Emit: prop.EmitConst},
		"Status":        {Value: "normal", Emit: prop.EmitConst},
		"IconThemePath": {Value: []string{}, Emit: prop.EmitConst},
	}}
	a.menuProps, err = prop.Export(a.conn, menuPath, menuMap)
	if err != nil {
		return err
	}
	menu := &trayMenu{app: a}
	if err := a.conn.Export(menu, menuPath, menuInterface); err != nil {
		return err
	}
	menuNode := &introspect.Node{Name: string(menuPath), Interfaces: []introspect.Interface{
		prop.IntrospectData,
		{
			Name:       menuInterface,
			Methods:    introspect.Methods(menu),
			Properties: a.menuProps.Introspection(menuInterface),
			Signals: []introspect.Signal{
				{Name: "LayoutUpdated", Args: []introspect.Arg{{Name: "revision", Type: "u"}, {Name: "parent", Type: "i"}}},
				{Name: "ItemsPropertiesUpdated", Args: []introspect.Arg{{Name: "updated", Type: "a(ia{sv})"}, {Name: "removed", Type: "a(ias)"}}},
			},
		},
	}}
	return a.conn.Export(introspect.NewIntrospectable(menuNode), menuPath, "org.freedesktop.DBus.Introspectable")
}

func statusItemPropertyMap(initialPixmaps []iconPixmap) map[string]*prop.Prop {
	return map[string]*prop.Prop{
		"Category":            {Value: "ApplicationStatus", Emit: prop.EmitConst},
		"Id":                  {Value: "unidrop", Emit: prop.EmitConst},
		"Title":               {Value: "UniDrop", Emit: prop.EmitFalse},
		"Status":              {Value: "Active", Emit: prop.EmitFalse},
		"WindowId":            {Value: uint32(0), Emit: prop.EmitConst},
		"IconName":            {Value: "", Emit: prop.EmitConst},
		"IconPixmap":          {Value: initialPixmaps, Emit: prop.EmitFalse},
		"OverlayIconName":     {Value: "", Emit: prop.EmitConst},
		"OverlayIconPixmap":   {Value: []iconPixmap{}, Emit: prop.EmitConst},
		"AttentionIconName":   {Value: "", Emit: prop.EmitConst},
		"AttentionIconPixmap": {Value: makeIconSet(iconAttention), Emit: prop.EmitFalse},
		"AttentionMovieName":  {Value: "", Emit: prop.EmitConst},
		"ToolTip":             {Value: toolTip{IconPixmap: initialPixmaps, Title: "UniDrop", Text: "Secure service is starting"}, Emit: prop.EmitFalse},
		"ItemIsMenu":          {Value: false, Emit: prop.EmitConst},
		"Menu":                {Value: menuPath, Emit: prop.EmitConst},
	}
}

func (a *trayApp) registerWatchers() bool {
	type watcher struct {
		service, iface, item string
	}
	watchers := []watcher{
		{"org.kde.StatusNotifierWatcher", "org.kde.StatusNotifierWatcher", a.traditionalName},
		{"org.freedesktop.StatusNotifierWatcher", "org.freedesktop.StatusNotifierWatcher", a.freedesktopName},
	}
	registered := false
	for _, watcher := range watchers {
		var owner string
		ownerCall := a.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").Call(
			"org.freedesktop.DBus.GetNameOwner", dbus.FlagNoAutoStart, watcher.service)
		if ownerCall.Err != nil || ownerCall.Store(&owner) != nil {
			delete(a.watcherOwners, watcher.service)
			continue
		}
		if a.watcherOwners[watcher.service] == owner {
			registered = true
			continue
		}
		call := a.conn.Object(watcher.service, "/StatusNotifierWatcher").Call(
			watcher.iface+".RegisterStatusNotifierItem", dbus.FlagNoAutoStart, watcher.item)
		if call.Err == nil {
			a.watcherOwners[watcher.service] = owner
			registered = true
		}
	}
	return registered
}

func requestName(conn *dbus.Conn, name string) error {
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("D-Bus name %s is already owned", name)
	}
	return nil
}

func run(baseURL string) error {
	client, err := newCoreClient(baseURL)
	if err != nil {
		return err
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to the desktop D-Bus session: %w", err)
	}
	defer conn.Close()

	pid := os.Getpid()
	traditionalName := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", pid)
	freedesktopName := fmt.Sprintf("org.freedesktop.StatusNotifierItem-%d-1", pid)
	if err := requestName(conn, traditionalName); err != nil {
		return err
	}
	if err := requestName(conn, freedesktopName); err != nil {
		log.Printf("new StatusNotifier name unavailable; using compatible KDE name: %v", err)
		freedesktopName = traditionalName
	}

	app := &trayApp{
		conn: conn, client: client, traditionalName: traditionalName, freedesktopName: freedesktopName,
		watcherOwners: make(map[string]string), summary: coreSummary{}, revision: 1, done: make(chan struct{}),
	}
	if err := app.export(); err != nil {
		return fmt.Errorf("export Linux tray interfaces: %w", err)
	}
	registered := app.registerWatchers()
	if registered {
		log.Printf("UniDrop %s tray registered", appVersion)
	} else {
		log.Printf("no StatusNotifier host found; UniDrop remains available from the application menu")
	}
	app.refreshSummary()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	refreshTicker := time.NewTicker(2 * time.Second)
	registerTicker := time.NewTicker(15 * time.Second)
	defer refreshTicker.Stop()
	defer registerTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-app.done:
			return nil
		case <-refreshTicker.C:
			app.refreshSummary()
		case <-registerTicker.C:
			wasRegistered := registered
			registered = app.registerWatchers()
			if !wasRegistered && registered {
				log.Printf("StatusNotifier host appeared; UniDrop tray registered")
			}
		}
	}
}

func main() {
	baseURL := flag.String("ui", defaultUIURL, "UniDrop loopback control-panel URL")
	showVersion := flag.Bool("version", false, "show version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("UniDrop Tray %s\n", appVersion)
		return
	}
	if err := run(*baseURL); err != nil {
		log.Fatal(err)
	}
}
