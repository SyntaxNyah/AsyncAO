package config

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestRaceHammerV2 is the §17.8 v2 race gate: four writers cycle through every single-scalar
// Set* method chosen by reflection (zero-value argument), while eight readers reflect over
// every Settings field through Load(). Run under `-race -count=20 -cpu 1,2,4,8` this is the
// torn-snapshot gate that TestConcurrentAccess (only six fixed mutators) does not cover.
func TestRaceHammerV2(t *testing.T) {
	p, _ := newTestPrefs(t)
	defer p.Close()

	typ := reflect.TypeOf(p)
	var setters []reflect.Method
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if !strings.HasPrefix(m.Name, "Set") || m.Type.NumIn() != 2 {
			continue
		}
		switch m.Type.In(1).Kind() {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64, reflect.String:
			setters = append(setters, m)
		}
	}
	if len(setters) == 0 {
		t.Fatal("no single-scalar Set* methods found")
	}
	t.Logf("cycling %d setters", len(setters))

	const (
		writers    = 4
		readers    = 8
		iterations = 300
	)
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				m := setters[(w*17+i)%len(setters)]
				m.Func.Call([]reflect.Value{reflect.ValueOf(p), reflect.Zero(m.Type.In(1))})
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s := p.Load()
				v := reflect.ValueOf(s).Elem()
				for f := 0; f < v.NumField(); f++ {
					_ = v.Field(f).Interface()
				}
			}
		}()
	}

	wg.Wait()
	if err := p.SaveNow(); err != nil {
		t.Fatalf("SaveNow after hammer: %v", err)
	}
}
