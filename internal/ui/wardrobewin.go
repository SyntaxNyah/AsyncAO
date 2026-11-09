package ui

// The Wardrobe window: the player's saved characters in folders, and the server's iniswaps.ini. What it lists comes
// from internal/ui/wardrobe; this file owns the window's state and actions, wardrobedraw.go paints it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ui/wardrobe"
)

const (
	// wardrobeListTimeout caps the iniswaps.ini download.
	wardrobeListTimeout = 15 * time.Second
	// wardrobeDragThreshold is the cursor travel (logical px) past which a press becomes a drag, as for tabs.
	wardrobeDragThreshold = tabDragThreshold
	// wardrobeAddLimit caps the names one Add takes.
	wardrobeAddLimit = 200
	// wardrobeImageMaxMB caps a folder image file; larger files are refused before decoding.
	wardrobeImageMaxMB    = 8
	wardrobeImageMaxBytes = wardrobeImageMaxMB << 20
	// Permissions for the image folder and the copies in it.
	wardrobeImageDirPerm  = 0o755
	wardrobeImageFilePerm = 0o644
	// wardrobeImageKeyPrefix keys folder images in the texture store; the scheme can't collide with asset URLs.
	wardrobeImageKeyPrefix = "wardrobe://"
)

// Drag kinds.
const (
	wardDragNone = iota
	wardDragChar
	wardDragFolder
)

// Folder image load states.
const (
	wardImageLoading = iota + 1
	wardImageLoaded
	wardImageFailed
)

// wardrobeViewKey is everything the shown list depends on; the list rebuilds only when it changes.
type wardrobeViewKey struct {
	rev        int64
	source     wardrobe.Source
	sort       wardrobe.Sort
	folder     string
	search     string
	serverKey  string
	slots      int
	serverList int
}

type wardrobeListFetch struct {
	key   string
	names []string
	err   error
}

type wardrobeImageLoad struct {
	name string
	dec  *assets.Decoded
	err  error
}

// wardrobeImageImport is a picked or dropped image copied into the image folder for folder id.
type wardrobeImageImport struct {
	folder string
	name   string
	err    error
}

// wardrobeWindow is the window's state; the App holds one as a.ward.
type wardrobeWindow struct {
	show     bool
	store    *config.Wardrobe
	storeRes chan *config.Wardrobe

	source   wardrobe.Source
	sort     wardrobe.Sort
	folder   string
	search   string
	listView bool

	key     wardrobeViewKey
	groups  []wardrobe.Group
	flat    []wardrobe.Item // every shown item in draw order: the icon cache's index space
	entries []config.WardrobeEntry
	folders []config.WardrobeFolder

	// iconPx is the grid's character icon size this frame, and accent and selected the highlight colour and a
	// selected card's fill, all from Settings.
	iconPx   int32
	accent   sdl.Color
	selected sdl.Color

	pages        []*render.TexturePage
	pagesGen     uint64
	ask          []time.Time
	scroll       int32
	drawerScroll int32
	// Built with the list, so painting allocates nothing: the drawer's rows, the toolbar's count, and each saved
	// character's folders as the List view shows them ("Defense +1").
	folderLabels map[string]string
	rows         []wardrobeDrawerRow
	countLabel   string
	groupHeads   []string

	sel           string
	confirmRemove bool
	nickOpen      bool
	nickText      string
	moveMenu      bool
	// moreMenu lists the bottom-bar actions that didn't fit (moreIDs and moreLabels, moreN of them) above moreAt.
	moreMenu   bool
	moreN      int
	moreIDs    [wardActCount]int
	moreLabels [wardActCount]string
	moreAt     sdl.Rect

	lookFor    string // folder whose look panel is open
	lookName   string
	renaming   string
	renameText string
	creating   bool
	newName    string
	deleting   string

	dragKind       int
	dragID         string
	dragging       bool
	pressX, pressY int32
	dropKind       int
	dropID         string
	dropOver       bool

	serverFor  string
	serverList []string
	serverBusy bool
	serverErr  string
	serverRes  chan wardrobeListFetch

	addOpen   bool
	addServer bool
	addText   string
	addPick   map[string]bool
	addTarget int // index into choices; 0 is Unfiled
	addScroll int32
	// addParsed is addText split into names, kept until the text changes.
	addParsed    []string
	addParsedFor string
	// choices is Unfiled then every folder's name, for the Add window's target and the move menu.
	choices []string

	// members is every saved character, lowercased, rebuilt when the store's revision moves.
	members    map[string]bool
	membersRev int64

	slots    map[string]int // lowercased character folder → server slot
	slotsFor string
	slotsN   int

	images    map[string]int // image file name → load state
	imageRes  chan wardrobeImageLoad
	importRes chan wardrobeImageImport
	pickRes   chan dialogPick
	pickFor   string // folder a file dialog is open for
}

// startWardrobeLoad reads wardrobe.json (or migrates the old lists) off the main thread; pollWardrobe lands it.
func (a *App) startWardrobeLoad() {
	w := &a.ward
	w.storeRes = make(chan *config.Wardrobe, 1)
	w.serverRes = make(chan wardrobeListFetch, 1)
	w.imageRes = make(chan wardrobeImageLoad, 1)
	w.importRes = make(chan wardrobeImageImport, 1)
	w.pickRes = make(chan dialogPick, 1)
	w.folder = wardrobe.AllFolders
	old := a.d.Prefs.Load()
	ch := w.storeRes
	go func() {
		if s, err := config.LoadWardrobe(old); err == nil {
			ch <- s
			PushWake()
		}
	}()
}

// CloseWardrobe writes any pending change on exit; a no-op until the file has loaded.
func (a *App) CloseWardrobe() {
	if a.ward.store != nil {
		_ = a.ward.store.Close()
	}
}

// openWardrobe shows the window and starts the server's list fetch once per server.
func (a *App) openWardrobe() {
	w := &a.ward
	w.show = true
	w.confirmRemove, w.nickOpen, w.moveMenu = false, false, false
	a.fetchWardrobeServerList()
}

// closeWardrobe hides the window and drops every half-finished edit in it.
func (a *App) closeWardrobe() {
	w := &a.ward
	w.show = false
	w.addOpen, w.confirmRemove, w.nickOpen, w.moveMenu, w.moreMenu = false, false, false, false, false
	w.lookFor, w.renaming, w.deleting, w.creating = "", "", "", false
	w.dragKind, w.dragging = wardDragNone, false
}

// closeWardrobeTop peels one layer for Esc: an open dialog, menu or editor first, then the window.
func (a *App) closeWardrobeTop() {
	w := &a.ward
	switch {
	case w.addOpen:
		w.addOpen = false
	case w.moveMenu || w.moreMenu || w.nickOpen || w.confirmRemove:
		w.moveMenu, w.moreMenu, w.nickOpen, w.confirmRemove = false, false, false, false
	case w.lookFor != "" || w.renaming != "" || w.deleting != "" || w.creating:
		w.lookFor, w.renaming, w.deleting, w.creating = "", "", "", false
	default:
		a.closeWardrobe()
	}
}

// pollWardrobe lands the store load, the server list, folder images and image imports.
func (a *App) pollWardrobe() {
	w := &a.ward
	if w.store == nil {
		select {
		case s := <-w.storeRes:
			w.store = s
		default:
		}
	}
	select {
	case res := <-w.serverRes:
		if res.key == a.serverKey {
			w.serverBusy = false
			w.serverList, w.serverErr = res.names, ""
			if res.err != nil {
				w.serverErr = res.err.Error()
			}
		}
	default:
	}
	select {
	case res := <-w.imageRes:
		a.landWardrobeImage(res)
	default:
	}
	select {
	case res := <-w.importRes:
		if res.err != nil {
			a.wardWarn("Couldn't use that image: " + res.err.Error())
		} else if w.store != nil {
			w.store.SetFolderImage(res.folder, res.name)
		}
	default:
	}
	select {
	case pick := <-w.pickRes:
		if pick.err == nil && len(pick.paths) > 0 && w.pickFor != "" {
			a.importWardrobeImage(w.pickFor, pick.paths[0])
		}
		w.pickFor = ""
	default:
	}
}

func (a *App) wardWarn(line string) {
	a.warnLine = clampLine(line)
	a.warnAt = time.Now()
}

// fetchWardrobeServerList fetches <origin>/iniswaps.ini once per server; a server without one shows no Server list.
func (a *App) fetchWardrobeServerList() {
	w := &a.ward
	if w.serverFor == a.serverKey || a.urls.Origin() == "" {
		return
	}
	w.serverFor, w.serverList, w.serverErr, w.serverBusy = a.serverKey, nil, "", true
	if w.source == wardrobe.SourceServer {
		w.source = wardrobe.SourceUser
	}
	url, key, mgr, ch := a.urls.Origin()+wardrobe.ServerListFileName, a.serverKey, a.d.Manager, w.serverRes
	go func() {
		defer func() {
			if r := recover(); r != nil {
				writeCrashLog("wardrobe list goroutine panic: ", r)
				ch <- wardrobeListFetch{key: key, err: fmt.Errorf("%v", r)}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), wardrobeListTimeout)
		defer cancel()
		data, err := mgr.FetchRaw(ctx, url)
		if err != nil {
			ch <- wardrobeListFetch{key: key, err: err}
		} else {
			ch <- wardrobeListFetch{key: key, names: wardrobe.ParseServerList(data)}
		}
		PushWake()
	}()
}

// wardrobeHas reports whether a lowercased character folder is saved; Character Select asks it for every cell.
func (a *App) wardrobeHas(lower string) bool {
	w := &a.ward
	if w.store == nil {
		return false
	}
	if rev := w.store.Rev(); w.members == nil || rev != w.membersRev {
		if w.members == nil {
			w.members = map[string]bool{}
		}
		clear(w.members)
		entries, _ := w.store.Snapshot()
		for _, e := range entries {
			w.members[strings.ToLower(e.Char)] = true
		}
		w.membersRev = rev
	}
	return w.members[lower]
}

// wardrobeSlot is the server slot of a character folder, ignoring case, or -1.
func (a *App) wardrobeSlot(char string) int {
	w := &a.ward
	if a.sess == nil {
		return -1
	}
	if w.slots == nil || w.slotsFor != a.serverKey || w.slotsN != len(a.sess.Chars) {
		w.slots = make(map[string]int, len(a.sess.Chars))
		for i := range a.sess.Chars {
			w.slots[strings.ToLower(a.sess.Chars[i].Name)] = i
		}
		w.slotsFor, w.slotsN = a.serverKey, len(a.sess.Chars)
	}
	if i, ok := w.slots[strings.ToLower(char)]; ok {
		return i
	}
	return -1
}

// refreshWardrobeView rebuilds the shown list when anything it depends on changed.
func (a *App) refreshWardrobeView() {
	w := &a.ward
	if w.store == nil {
		return
	}
	a.wardrobeSlot("") // refresh the slot table first, so the key below sees its size
	key := wardrobeViewKey{
		rev: w.store.Rev(), source: w.source, sort: w.sort, folder: w.folder, search: w.search,
		serverKey: a.serverKey, slots: w.slotsN, serverList: len(w.serverList),
	}
	if key == w.key && w.flat != nil {
		return
	}
	w.key = key
	w.entries, w.folders = w.store.Snapshot()
	if w.folder != wardrobe.AllFolders && w.folder != wardrobe.Unfiled && a.wardrobeFolderIndex(w.folder) < 0 {
		w.folder = wardrobe.AllFolders // the open folder was deleted
		w.key.folder = w.folder
	}
	onServer := func(c string) bool { return a.wardrobeSlot(c) >= 0 }
	w.groups = wardrobe.Build(w.entries, w.folders, w.serverList, onServer,
		wardrobe.Query{Source: w.source, Folder: w.folder, Search: w.search, Sort: w.sort})
	w.flat = w.flat[:0]
	w.groupHeads = w.groupHeads[:0]
	for _, g := range w.groups {
		w.flat = append(w.flat, g.Items...)
		w.groupHeads = append(w.groupHeads, strings.ToUpper(a.wardrobeFolderName(g.Folder))+"  "+strconv.Itoa(len(g.Items)))
	}
	w.countLabel = strconv.Itoa(len(w.flat)) + " characters"
	if len(w.flat) == 1 {
		w.countLabel = "1 character"
	}
	w.rows = a.wardrobeDrawerRows()
	if w.folderLabels == nil {
		w.folderLabels = map[string]string{}
	}
	clear(w.folderLabels)
	for _, e := range w.entries {
		label := "Unfiled"
		if len(e.Folders) > 0 {
			label = a.wardrobeFolderName(e.Folders[0])
			if more := len(e.Folders) - 1; more > 0 {
				label += " +" + strconv.Itoa(more)
			}
		}
		w.folderLabels[e.Char] = label
	}
	w.choices = a.wardrobeFolderChoices()
	// cachedPage keys pages by index without re-checking the URL, so a reorder must drop them.
	w.pages, w.ask = nil, nil
}

func (a *App) wardrobeFolderIndex(id string) int {
	for i, f := range a.ward.folders {
		if f.ID == id {
			return i
		}
	}
	return -1
}

// wardrobeFolderName is a folder id's name, "Unfiled" for none.
func (a *App) wardrobeFolderName(id string) string {
	if i := a.wardrobeFolderIndex(id); i >= 0 {
		return a.ward.folders[i].Name
	}
	return "Unfiled"
}

// wardrobeSelected is the selected item in the shown list.
func (a *App) wardrobeSelected() (wardrobe.Item, bool) {
	for _, it := range a.ward.flat {
		if it.Char == a.ward.sel {
			return it, true
		}
	}
	return wardrobe.Item{}, false
}

// wardrobeCanSwitch reports whether char has a free server slot that isn't already ours.
func (a *App) wardrobeCanSwitch(char string) bool {
	slot := a.wardrobeSlot(char)
	ok, _ := wardrobeUseChoice(slot, slot >= 0 && a.sess.Chars[slot].Taken, slot >= 0 && slot == a.sess.MyCharID)
	return ok
}

// wardrobeSwitch takes char's server slot.
func (a *App) wardrobeSwitch(char string) {
	slot := a.wardrobeSlot(char)
	if slot < 0 {
		return
	}
	a.markWardrobeWorn(char)
	a.closeWardrobe()
	a.pickCharacter(slot) // CC → PV → enterCourtroom, which clears any worn iniswap
}

// wardrobeWear wears char as an iniswap without taking a slot.
func (a *App) wardrobeWear(char string) {
	a.markWardrobeWorn(char)
	a.closeWardrobe()
	a.setIniswap(char)
}

// wardrobeUseChoice is what double-click does with a character: switch when its slot exists, is free and isn't ours,
// else wear it, with the reason.
func wardrobeUseChoice(slot int, taken, mine bool) (switchSlot bool, why string) {
	switch {
	case slot < 0:
		return false, " isn't a character on this server"
	case mine:
		return false, " is already your character"
	case taken:
		return false, " is taken"
	}
	return true, ""
}

// wardrobeUse is double-click and Enter: switch when the slot is free, else wear it, saying which.
func (a *App) wardrobeUse(char string) {
	slot := a.wardrobeSlot(char)
	taken := slot >= 0 && a.sess.Chars[slot].Taken
	switchSlot, why := wardrobeUseChoice(slot, taken, slot >= 0 && slot == a.sess.MyCharID)
	if switchSlot {
		a.wardrobeSwitch(char)
		a.wardWarn("Switched to " + char)
		return
	}
	a.wardrobeWear(char)
	a.wardWarn(char + why + ", so it's worn as an iniswap")
}

func (a *App) markWardrobeWorn(char string) {
	if a.ward.store != nil {
		a.ward.store.MarkWorn(char, time.Now())
	}
}

// wardrobeFolderChoices are the Add window's targets: Unfiled, then the folders in order.
func (a *App) wardrobeFolderChoices() []string {
	out := make([]string, 0, len(a.ward.folders)+1)
	out = append(out, "Unfiled")
	for _, f := range a.ward.folders {
		out = append(out, f.Name)
	}
	return out
}

// wardrobeChoiceFolder maps a folder-choice index back to a folder id.
func (a *App) wardrobeChoiceFolder(i int) string {
	if i <= 0 || i > len(a.ward.folders) {
		return wardrobe.Unfiled
	}
	return a.ward.folders[i-1].ID
}

// startWardrobeDrag arms a press on a card or folder; it becomes a drag past wardrobeDragThreshold.
func (a *App) startWardrobeDrag(kind int, id string) {
	c := a.ctx
	w := &a.ward
	w.dragKind, w.dragID, w.dragging = kind, id, false
	w.pressX, w.pressY = c.mouseX, c.mouseY
}

// updateWardrobeDrag runs once per frame before drawing: it promotes a press to a drag and clears last frame's target.
func (a *App) updateWardrobeDrag() {
	c := a.ctx
	w := &a.ward
	w.dropKind, w.dropID, w.dropOver = wardDragNone, "", false
	if w.dragKind == wardDragNone {
		return
	}
	if !w.dragging && c.mouseDown {
		dx, dy := c.mouseX-w.pressX, c.mouseY-w.pressY
		if dx*dx+dy*dy > wardrobeDragThreshold*wardrobeDragThreshold {
			w.dragging = true
		}
	}
}

// offerWardrobeDrop marks r as where the dragged thing would land this frame, when the pointer is over it.
func (a *App) offerWardrobeDrop(r sdl.Rect, kind int, id string) bool {
	w := &a.ward
	if !w.dragging || !a.ctx.hovering(r) {
		return false
	}
	w.dropKind, w.dropID, w.dropOver = kind, id, true
	return true
}

// finishWardrobeDrag runs after drawing: on release it applies the drop the frame found, and a release without a
// drag leaves the click to the widgets that already saw it.
func (a *App) finishWardrobeDrag() {
	c := a.ctx
	w := &a.ward
	if w.dragKind == wardDragNone || c.mouseDown {
		return
	}
	if w.dragging && w.dropOver && w.store != nil {
		switch {
		case w.dragKind == wardDragChar && w.dropKind == wardDragChar:
			w.store.Move(w.dragID, w.dropID)
		case w.dragKind == wardDragChar && w.dropKind == wardDragFolder:
			a.dropWardrobeOnFolder(w.dragID, w.dropID)
		case w.dragKind == wardDragFolder && w.dropKind == wardDragFolder:
			w.store.MoveFolder(w.dragID, w.dropID)
		}
	}
	w.dragKind, w.dragID, w.dragging = wardDragNone, "", false
}

// dropWardrobeOnFolder files a dragged character onto a drawer row: from All favourites or Unfiled it adds that
// folder (Unfiled clears them all); from inside a folder it moves out of that folder into the target.
func (a *App) dropWardrobeOnFolder(char, target string) {
	w := &a.ward
	switch {
	case w.folder == wardrobe.AllFolders || w.folder == wardrobe.Unfiled:
		if target == wardrobe.Unfiled {
			w.store.ClearFolders(char)
		} else {
			w.store.AddToFolder(char, target)
		}
	default:
		w.store.MoveToFolder(char, w.folder, target)
	}
}

// wardrobeImagePage is folder image name's texture, loading it off-thread on first use.
func (a *App) wardrobeImagePage(name string) (*render.TexturePage, bool) {
	w := &a.ward
	if name == "" || a.d.Store == nil {
		return nil, false
	}
	key := wardrobeImageKeyPrefix + name
	if page, ok := a.d.Store.Get(key); ok && len(page.Frames) > 0 {
		return page, true
	}
	if w.images == nil {
		w.images = map[string]int{}
	}
	switch w.images[name] {
	case wardImageLoading, wardImageFailed:
		return nil, false
	}
	if len(w.images) >= len(w.folders)+1 {
		clear(w.images) // bounded by the folder count; an evicted image simply loads again
	}
	w.images[name] = wardImageLoading
	ch := w.imageRes
	go func() {
		res := wardrobeImageLoad{name: name}
		dir, err := config.WardrobeImageDir()
		if err == nil {
			var b []byte
			if b, err = os.ReadFile(filepath.Join(dir, name)); err == nil {
				res.dec, err = assets.DecodeImage(b, false)
			}
		}
		res.err = err
		ch <- res
		PushWake()
	}()
	return nil, false
}

func (a *App) landWardrobeImage(res wardrobeImageLoad) {
	w := &a.ward
	if w.images == nil {
		w.images = map[string]int{}
	}
	if res.err != nil || res.dec == nil || a.d.Store == nil {
		w.images[res.name] = wardImageFailed
		return
	}
	if err := a.d.Store.UploadSmall(wardrobeImageKeyPrefix+res.name, res.dec); err != nil {
		w.images[res.name] = wardImageFailed
		return
	}
	w.images[res.name] = wardImageLoaded
}

// isWardrobeImageFile reports a file name the folder images accept.
func isWardrobeImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".webp", ".gif":
		return true
	}
	return false
}

// importWardrobeImage copies an image into the Wardrobe's image folder off-thread, so the folder keeps its icon when
// the original moves; pollWardrobe then sets it on the folder.
func (a *App) importWardrobeImage(folder, path string) {
	if !isWardrobeImageFile(path) {
		a.wardWarn("Folder images must be PNG, WebP or GIF files")
		return
	}
	ch := a.ward.importRes
	go func() {
		res := wardrobeImageImport{folder: folder}
		res.err = func() error {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.Size() > wardrobeImageMaxBytes {
				return fmt.Errorf("the file is over %d MB", wardrobeImageMaxMB)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := assets.DecodeImage(b, false); err != nil {
				return err
			}
			dir, err := config.WardrobeImageDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, wardrobeImageDirPerm); err != nil {
				return err
			}
			res.name = folder + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + strings.ToLower(filepath.Ext(path))
			return os.WriteFile(filepath.Join(dir, res.name), b, wardrobeImageFilePerm)
		}()
		ch <- res
		PushWake()
	}()
}

// browseWardrobeImage opens the file dialog for folder id's image.
func (a *App) browseWardrobeImage(folder string) {
	w := &a.ward
	if w.pickFor != "" {
		a.wardWarn("The file picker is already open, possibly behind this window.")
		return
	}
	w.pickFor = folder
	ch := w.pickRes
	a.d.Desktop.PickFiles(sdl.DialogOptions{
		Window:  a.ctx.win,
		Title:   "Pick a folder image",
		Filters: wardrobeImageFilters,
	}, func(paths []string, err error) {
		ch <- dialogPick{paths: paths, err: err}
		PushWake()
	})
}

var wardrobeImageFilters = []sdl.DialogFilter{{Name: "Images", Pattern: "png;webp;gif"}}
