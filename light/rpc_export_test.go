/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"reflect"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/scdoproject/go-scdo/rpc"
)

// TestLightRPCServicesAreExported is the check rpc.Server.RegisterName applies
// at node start. An unexported service type aborts the process with
// "headerProofAPI is not exported".
func TestLightRPCServicesAreExported(t *testing.T) {
	server := &ServiceServer{scdoProtocol: &LightProtocol{chain: nil}}
	assertServicesExported(t, server.APIs())

	phone := NewMultiService(nil)
	assertServicesExported(t, phone.APIs())
}

func assertServicesExported(t *testing.T, apis []rpc.API) {
	t.Helper()
	if len(apis) == 0 {
		t.Fatal("no RPC services registered")
	}
	srv := rpc.NewServer()
	defer srv.Stop()
	for _, api := range apis {
		if api.Service == nil {
			t.Fatalf("namespace %s has a nil service", api.Namespace)
		}
		name := rpcServiceName(api.Service)
		r, _ := utf8.DecodeRuneInString(name)
		if name == "" || !unicode.IsUpper(r) {
			t.Fatalf("RPC service %q in namespace %s is not exported", name, api.Namespace)
		}
		if err := srv.RegisterName(api.Namespace, api.Service); err != nil {
			t.Fatalf("register %s.%s: %s", api.Namespace, name, err)
		}
	}
}

func rpcServiceName(service interface{}) string {
	return reflect.Indirect(reflect.ValueOf(service)).Type().Name()
}
