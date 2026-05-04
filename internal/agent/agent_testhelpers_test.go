package agent

import (
	"reflect"

	"github.com/eight-acres-lab/openmelon/internal/llm"
)

// setBaseURLForTest pokes the unexported `baseURL` field on an Anthropic
// or OpenAI client so tests can point it at httptest servers.
//
// Production code never calls this — it lives in a non-_test.go file
// only because Go's test packages cannot reach unexported fields in
// other packages. Keeping the reflection in one helper avoids
// scattering reflect calls through the test file.
func setBaseURLForTest(c llm.Client, url string) {
	v := reflect.ValueOf(c).Elem()
	f := v.FieldByName("baseURL")
	if !f.IsValid() {
		return
	}
	// Bypass unexported-field assignment guard via unsafe pointer trick.
	// Acceptable because this file is build-only-for-tests by intent.
	rf := reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Elem()
	rf.SetString(url)
}
