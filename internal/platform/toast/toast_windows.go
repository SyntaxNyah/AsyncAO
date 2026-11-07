package toast

import (
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// combase and advapi32 are KnownDLLs, so Windows loads them from System32 whatever the search path holds.
var (
	combase                    = syscall.NewLazyDLL("combase.dll")
	procRoInitialize           = combase.NewProc("RoInitialize")
	procRoUninitialize         = combase.NewProc("RoUninitialize")
	procRoActivateInstance     = combase.NewProc("RoActivateInstance")
	procRoGetActivationFactory = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString    = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString    = combase.NewProc("WindowsDeleteString")

	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	procRegCreateKeyExW = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
)

const (
	classXMLDocument  = "Windows.Data.Xml.Dom.XmlDocument"
	classToast        = "Windows.UI.Notifications.ToastNotification"
	classToastManager = "Windows.UI.Notifications.ToastNotificationManager"

	// appIDKey is where an unpackaged app registers the name its notifications show.
	appIDKey       = `Software\Classes\AppUserModelId\`
	displayNameVal = "DisplayName"
	iconURIVal     = "IconUri"
	iconFile       = "notification.ico"
	iconDirMode    = 0o755
	iconFileMode   = 0o644

	// toastTemplate takes the escaped title and body.
	toastTemplate = `<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>`

	roInitMultithreaded = 1
	// sFalse is RoInitialize's success code for a thread already initialized.
	sFalse = 1

	// Vtable indexes: IUnknown has three methods and IInspectable three more, so an interface's own methods start at 6.
	methodQueryInterface      = 0
	methodRelease             = 2
	methodLoadXML             = 6 // IXmlDocumentIO
	methodCreateToast         = 6 // IToastNotificationFactory
	methodCreateNotifierForID = 7 // IToastNotificationManagerStatics
	methodShow                = 6 // IToastNotifier

	hresultFormat = "%s: HRESULT 0x%08X"
)

// Interface IDs from windows.data.xml.dom.h and windows.ui.notifications.h.
//
//nolint:mnd // GUID fields as the Windows SDK headers publish them.
var (
	iidXMLDocumentIO       = syscall.GUID{Data1: 0x6cd0e74e, Data2: 0xee65, Data3: 0x4489, Data4: [8]byte{0x9e, 0xbf, 0xca, 0x43, 0xe8, 0x7b, 0xa6, 0x37}}
	iidXMLDocument         = syscall.GUID{Data1: 0xf7f3a506, Data2: 0x1e87, Data3: 0x42d6, Data4: [8]byte{0xbc, 0xfb, 0xb8, 0xc8, 0x09, 0xfa, 0x54, 0x94}}
	iidToastFactory        = syscall.GUID{Data1: 0x04124b20, Data2: 0x82c6, Data3: 0x4229, Data4: [8]byte{0xb1, 0x09, 0xfd, 0x9e, 0xd4, 0x66, 0x2b, 0x53}}
	iidToastManagerStatics = syscall.GUID{Data1: 0x50ac103f, Data2: 0xd235, Data3: 0x4598, Data4: [8]byte{0xbb, 0xef, 0x98, 0xfe, 0x4d, 0x1a, 0x3a, 0xd4}}
)

// winSender talks to WinRT directly; it starts WinRT and registers the app on the first notification.
type winSender struct {
	app         App
	initialized bool
	factory     unsafe.Pointer // IToastNotificationFactory
	notifier    unsafe.Pointer // IToastNotifier for app.ID
}

func newSender(app App) sender { return &winSender{app: app} }

func (s *winSender) send(title, body string) error {
	if s.notifier == nil {
		if err := s.start(); err != nil {
			return err
		}
	}
	doc, err := activate(classXMLDocument)
	if err != nil {
		return err
	}
	defer release(doc)
	docIO, err := queryInterface(doc, &iidXMLDocumentIO)
	if err != nil {
		return err
	}
	defer release(docIO)
	content, err := newHString(toastXML(title, body))
	if err != nil {
		return err
	}
	defer deleteHString(content)
	if err := call(docIO, methodLoadXML, content); err != nil {
		return fmt.Errorf("load toast xml: %w", err)
	}
	xmlDoc, err := queryInterface(doc, &iidXMLDocument)
	if err != nil {
		return err
	}
	defer release(xmlDoc)
	var toast unsafe.Pointer
	if err := call(s.factory, methodCreateToast, uintptr(xmlDoc), uintptr(unsafe.Pointer(&toast))); err != nil {
		return fmt.Errorf("create toast: %w", err)
	}
	defer release(toast)
	return call(s.notifier, methodShow, uintptr(toast))
}

// start initializes WinRT on this thread, registers app.ID's display name and keeps the factory and notifier.
func (s *winSender) start() error {
	if !s.initialized {
		hr, _, _ := procRoInitialize.Call(roInitMultithreaded)
		if hr != 0 && hr != sFalse {
			return hresultError("RoInitialize", hr)
		}
		s.initialized = true
	}
	if err := registerApp(s.app); err != nil {
		return err
	}
	if s.factory == nil {
		f, err := activationFactory(classToast, &iidToastFactory)
		if err != nil {
			return err
		}
		s.factory = f
	}
	manager, err := activationFactory(classToastManager, &iidToastManagerStatics)
	if err != nil {
		return err
	}
	defer release(manager)
	id, err := newHString(s.app.ID)
	if err != nil {
		return err
	}
	defer deleteHString(id)
	var notifier unsafe.Pointer
	if err := call(manager, methodCreateNotifierForID, id, uintptr(unsafe.Pointer(&notifier))); err != nil {
		return fmt.Errorf("toast notifier: %w", err)
	}
	s.notifier = notifier
	return nil
}

func (s *winSender) close() {
	release(s.notifier)
	release(s.factory)
	s.notifier, s.factory = nil, nil
	if s.initialized {
		_, _, _ = procRoUninitialize.Call()
		s.initialized = false
	}
}

// registerApp writes the name and icon Windows shows for app.ID; without it Windows drops an unpackaged app's
// notifications. A missing icon only costs the picture, so its failure is logged, not returned.
func registerApp(app App) error {
	key, err := syscall.UTF16PtrFromString(appIDKey + app.ID)
	if err != nil {
		return err
	}
	var h syscall.Handle
	if rc, _, _ := procRegCreateKeyExW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(key)), 0, 0, 0,
		syscall.KEY_SET_VALUE, 0, uintptr(unsafe.Pointer(&h)), 0); rc != 0 {
		return fmt.Errorf("register app id: %w", syscall.Errno(rc))
	}
	defer syscall.RegCloseKey(h)
	if err := setString(h, displayNameVal, app.Name); err != nil {
		return err
	}
	if len(app.Icon) == 0 {
		return nil
	}
	icon, err := writeIcon(app)
	if err == nil {
		err = setString(h, iconURIVal, icon)
	}
	if err != nil {
		log.Printf("toast: icon: %v", err)
	}
	return nil
}

// writeIcon saves app.Icon in the per-user cache, as Windows reads a notification's icon from a file.
func writeIcon(app App) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, app.Name)
	if err := os.MkdirAll(dir, iconDirMode); err != nil {
		return "", err
	}
	path := filepath.Join(dir, iconFile)
	return path, os.WriteFile(path, app.Icon, iconFileMode)
}

func setString(key syscall.Handle, name, value string) error {
	valueName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	data, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	size := uintptr(len(data)) * unsafe.Sizeof(data[0])
	if rc, _, _ := procRegSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(valueName)), 0, syscall.REG_SZ,
		uintptr(unsafe.Pointer(&data[0])), size); rc != 0 {
		return fmt.Errorf("register %s: %w", name, syscall.Errno(rc))
	}
	return nil
}

// toastXML builds the notification; escaping keeps a crafted title or body inside its text element.
func toastXML(title, body string) string {
	return fmt.Sprintf(toastTemplate, escapeXML(title), escapeXML(body))
}

func escapeXML(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s)) // a strings.Builder never fails to write
	return b.String()
}

// call invokes the method at index on a COM interface; WinRT methods return an HRESULT. uintptrescapes keeps a Go
// pointer passed as a uintptr alive and in place until the call returns.
//
//go:uintptrescapes
func call(obj unsafe.Pointer, index uintptr, args ...uintptr) error {
	vtable := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtable, index*unsafe.Sizeof(uintptr(0))))
	hr, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	if int32(hr) < 0 {
		return hresultError("call", hr)
	}
	return nil
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		_ = call(obj, methodRelease)
	}
}

func queryInterface(obj unsafe.Pointer, iid *syscall.GUID) (unsafe.Pointer, error) {
	var out unsafe.Pointer
	if err := call(obj, methodQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); err != nil {
		return nil, fmt.Errorf("query interface: %w", err)
	}
	return out, nil
}

func activate(class string) (unsafe.Pointer, error) {
	name, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer deleteHString(name)
	var out unsafe.Pointer
	if hr, _, _ := procRoActivateInstance.Call(name, uintptr(unsafe.Pointer(&out))); int32(hr) < 0 {
		return nil, hresultError("activate "+class, hr)
	}
	return out, nil
}

func activationFactory(class string, iid *syscall.GUID) (unsafe.Pointer, error) {
	name, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer deleteHString(name)
	var out unsafe.Pointer
	if hr, _, _ := procRoGetActivationFactory.Call(name, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); int32(hr) < 0 {
		return nil, hresultError("factory for "+class, hr)
	}
	return out, nil
}

// newHString copies s into a WinRT string; the caller deletes it.
func newHString(s string) (uintptr, error) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h uintptr
	if hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h))); int32(hr) < 0 {
		return 0, hresultError("WindowsCreateString", hr)
	}
	return h, nil
}

func deleteHString(h uintptr) { _, _, _ = procWindowsDeleteString.Call(h) }

func hresultError(what string, hr uintptr) error { return fmt.Errorf(hresultFormat, what, uint32(hr)) }
