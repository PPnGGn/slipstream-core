package android

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	testSocket = "/data/user/0/com.slipstream/files/http.sock"
	testUser   = "u9f31c2a47d0e5b68"
	testPass   = "p4c8e1d7a5b2093f6e8d14a7c0b3592e"
)

// LocalProxy carries the password of the session's proxy. The gobind class of a
// struct with exported fields has a toString that prints all of them, and the
// password then goes wherever the app logs the object. The credentials are now
// behind methods, and whatever a program makes of the object as text leaves them
// out. (Not JSON: with no exported field json.Marshal gives {} by the rules of the
// language, and staticcheck rightly objects to a test that marshals such a type.)
func TestLocalProxyHidesItsCredentialsInEveryFormat(t *testing.T) {
	p := newLocalProxy(testSocket, testUser, testPass)

	var outputs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%10.3v", "%t"} {
		outputs = append(outputs, fmt.Sprintf(verb, p), fmt.Sprintf(verb, *p))
	}
	outputs = append(outputs, p.String(), fmt.Sprint(p), fmt.Sprint(*p), fmt.Sprintln(p), fmt.Sprintf("%v", []*LocalProxy{p}))

	for _, out := range outputs {
		for name, secret := range map[string]string{"password": testPass, "user": testUser} {
			// The text itself, and hex of it (what %x prints for a string field).
			if strings.Contains(out, secret) || strings.Contains(out, hex.EncodeToString([]byte(secret))) {
				t.Errorf("the %s is in the text of the object: %q", name, out)
			}
		}
	}
}

// What the object says about itself is still of use when something goes wrong:
// where the socket is.
func TestLocalProxyStringNamesTheSocket(t *testing.T) {
	p := newLocalProxy(testSocket, testUser, testPass)
	for _, text := range []string{p.String(), fmt.Sprint(p), fmt.Sprintf("%+v", p)} {
		if !strings.Contains(text, testSocket) {
			t.Errorf("%q does not name the socket", text)
		}
	}
}

func TestLocalProxyGivesTheCredentialsThroughMethods(t *testing.T) {
	p := newLocalProxy(testSocket, testUser, testPass)

	if p.Socket() != testSocket || p.User() != testUser || p.Pass() != testPass {
		t.Errorf("Socket, User, Pass = %q, %q, %q; want what the proxy was made with", p.Socket(), p.User(), p.Pass())
	}
}

// gomobile binds exported fields as getters and setters and prints them in
// toString; an unexported field is not bound at all.
func TestLocalProxyHasNoExportedFields(t *testing.T) {
	typ := reflect.TypeOf(LocalProxy{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Errorf("field %s is exported: gomobile would bind it and print it in toString", typ.Field(i).Name)
		}
	}
}

// No session, no proxy: the nil the app checks for.
func TestLocalHTTPProxyIsNilWithoutASession(t *testing.T) {
	if got := LocalHTTPProxy(); got != nil {
		t.Errorf("LocalHTTPProxy() = %v with no session, want nil", got)
	}
}
