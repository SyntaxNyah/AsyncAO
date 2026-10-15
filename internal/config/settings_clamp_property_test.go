package config

import (
	"reflect"
	"strings"
	"testing"
)

// setterFields returns the data field(s) a Set* method writes, derived by applying the
// "valid" scenario to a default prefs and reading the changed leaf fields.
func setterFields(m reflect.Method) []string {
	base := defaultPrefs("")
	after := defaultPrefs("")
	args := make([]reflect.Value, m.Type.NumIn()-1)
	for i := 1; i < m.Type.NumIn(); i++ {
		args[i-1] = validOf(m.Type.In(i))
	}
	call := append([]reflect.Value{reflect.ValueOf(after)}, args...)
	m.Func.Call(call)
	diff := fieldDiff(base, after)
	var out []string
	for _, d := range diff {
		name := d
		if i := strings.Index(name, "="); i >= 0 {
			name = name[:i]
		}
		out = append(out, name)
	}
	return out
}

// setSettingsField assigns v to the named Settings field via reflection.
func setSettingsField(s *Settings, name string, v reflect.Value) {
	f := reflect.ValueOf(s).Elem().FieldByName(name)
	if f.IsValid() && f.CanSet() && v.IsValid() {
		if v.Type().AssignableTo(f.Type()) {
			f.Set(v)
		} else if v.Type().ConvertibleTo(f.Type()) {
			f.Set(v.Convert(f.Type()))
		}
	}
}

// staysExplicit lists the numeric single-field setters whose clamp is not a simple range
// (an enum whitelist or a one-sided clamp), so they cannot be expressed as one field
// assignment through Update and stay explicit for now.
var staysExplicit = map[string]bool{
	"SetAssetCharCasing":   true, // enum whitelist
	"SetChromeShapeTier":   true, // one-sided (high only)
	"SetReflectStrength":   true, // one-sided (high only)
	"SetThemeFit":          true, // one-sided + one-shot stamp
	"SetWeatherType":       true, // one-sided (high only)
	"SetSpriteLoadMode":    true, // enum whitelist
	"SetTexCompression":    true, // enum whitelist
	"SetProxyMode":         true, // enum whitelist
	"SetSpriteScalingMode": true, // enum whitelist
}

// TestSetterGoldenViaUpdate runs every EXPRESSIBLE setter golden case through Update: the
// setter is applied normally (the golden), and the same scenario's args are assigned to the
// setter's field through Update; the whole-struct diff must match. Single-field numeric
// setters only; the sentinel "zero"/slice/string/enum cases stay explicit (listed by the
// skip). Multi-field and no-field setters stay explicit too.
func TestSetterGoldenViaUpdate(t *testing.T) {
	pt := reflect.TypeOf(&AssetPreferences{})
	checked := 0
	for i := 0; i < pt.NumMethod(); i++ {
		m := pt.Method(i)
		if !strings.HasPrefix(m.Name, "Set") || m.Type.NumIn() != 2 {
			continue
		}
		fields := setterFields(m)
		if len(fields) != 1 {
			continue
		}
		// only bool/numeric field assignments are expressible through Update; string setters
		// may sanitize or default (SetSubtheme/SetHoldClearKey) and stay explicit.
		switch m.Type.In(1).Kind() {
		case reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
		default:
			continue // string/slice/map/struct/ptr stay explicit
		}
		if staysExplicit[m.Name] {
			continue
		}
		for _, sc := range setterScenarios(m) {
			switch sc.label {
			case "zero", "nil", "empty":
				continue // sentinel/collection cases stay explicit
			}
			base := defaultPrefs("")
			after := defaultPrefs("")
			call := append([]reflect.Value{reflect.ValueOf(after)}, sc.args...)
			var panicked string
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = r.(string)
					}
				}()
				m.Func.Call(call)
			}()
			if panicked != "" {
				continue
			}
			want := fieldDiff(base, after)

			upd := defaultPrefs("")
			upd.Update(func(s *Settings) {
				for j, arg := range sc.args {
					setSettingsField(s, fields[j], arg)
				}
			})
			have := fieldDiff(base, upd)
			checked++
			if !reflect.DeepEqual(have, want) {
				t.Errorf("%s/%s: Update diff=%v, setter diff=%v", m.Name, sc.label, have, want)
			}
		}
	}
	t.Logf("checked %d setter scenarios via Update", checked)
}

// setIntVal sets v to i for int and uint kinds alike.
func setIntVal(v reflect.Value, i int64) {
	switch v.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(i))
	default:
		v.SetInt(i)
	}
}

// TestClampProperty is the (b) proof: for every single-field NUMERIC setter, out-of-range
// and boundary values assigned through Update must clamp to the same result as the setter.
func TestClampProperty(t *testing.T) {
	pt := reflect.TypeOf(&AssetPreferences{})
	checked := 0
	for i := 0; i < pt.NumMethod(); i++ {
		m := pt.Method(i)
		if !strings.HasPrefix(m.Name, "Set") || m.Type.NumIn() != 2 {
			continue
		}
		fields := setterFields(m)
		if len(fields) != 1 {
			continue
		}
		argT := m.Type.In(1)
		switch argT.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		default:
			continue
		}
		if staysExplicit[m.Name] {
			continue
		}
		for _, probe := range []int64{-1, 1, 2147483647} {
			val := reflect.New(argT).Elem()
			setIntVal(val, probe)
			after := defaultPrefs("")
			m.Func.Call([]reflect.Value{reflect.ValueOf(after), val})
			want := fieldDiff(defaultPrefs(""), after)
			upd := defaultPrefs("")
			upd.Update(func(s *Settings) { setSettingsField(s, fields[0], val) })
			have := fieldDiff(defaultPrefs(""), upd)
			checked++
			if !reflect.DeepEqual(have, want) {
				t.Errorf("%s(%d): Update diff=%v, setter diff=%v", m.Name, probe, have, want)
			}
		}
	}
	t.Logf("checked %d clamp probes", checked)
}
