package daemon

import (
	"context"
	"encoding/json"
	"testing"

	godap "github.com/google/go-dap"
	"github.com/y0geshpatil/sl-dbg/internal/proto"
)

func TestBreakFnPersistsAdapterVerification(t *testing.T) {
	for _, verified := range []bool{false, true} {
		server, sess := fakeSession(t, "java", false, func(request godap.RequestMessage) []godap.Message {
			bps := request.(*godap.SetFunctionBreakpointsRequest).Arguments.Breakpoints
			if bps[len(bps)-1].Name != "Buggy#compute" {
				t.Errorf("function name not normalized: %+v", bps)
			}
			results := make([]godap.Breakpoint, len(bps))
			for i := range results {
				results[i] = godap.Breakpoint{Id: 40 + i, Verified: verified}
			}
			return []godap.Message{&godap.SetFunctionBreakpointsResponse{
				Response: responseTo(request),
				Body:     godap.SetFunctionBreakpointsResponseBody{Breakpoints: results},
			}}
		})
		for i := 0; i < 2; i++ {
			result := server.handleBreakFn(context.Background(), proto.Request{
				Sess: sess.ID, Args: json.RawMessage(`{"function":"Buggy.compute"}`),
			})
			if !result.OK {
				t.Fatalf("break-fn: %+v", result)
			}
			var bp proto.BreakResult
			if err := json.Unmarshal(result.Data, &bp); err != nil {
				t.Fatal(err)
			}
			if bp.Verified != verified || bp.Function != "Buggy.compute" {
				t.Fatalf("response lost adapter verification: %+v", bp)
			}
			stored := sess.FuncBPs()
			for j, saved := range stored {
				if saved.Verified != verified || saved.DAPID != 40+j {
					t.Fatalf("stored verification not updated: %+v", stored)
				}
			}
			var list proto.BreaksResult
			if err := json.Unmarshal(server.handleBreaks(proto.Request{Sess: sess.ID}).Data, &list); err != nil {
				t.Fatal(err)
			}
			if list.Breakpoints[i].Verified != verified {
				t.Fatalf("breaks lost adapter verification: %+v", list)
			}
		}
	}
}

func TestNormalizeFuncBPName(t *testing.T) {
	cases := []struct {
		name string
		lang string
		in   string
		want string
	}{
		{"java-bare-class-method", "java", "Buggy.fib", "Buggy#fib"},
		{"java-fq-class-method", "java", "com.example.Foo.bar", "com.example.Foo#bar"},
		{"java-already-hash", "java", "Buggy#fib", "Buggy#fib"},
		{"java-fq-already-hash", "java", "com.example.Foo#bar", "com.example.Foo#bar"},
		{"java-strips-parens", "java", "Buggy.fib(int)", "Buggy#fib"},
		{"java-strips-parens-with-hash", "java", "com.example.Foo#bar(int,int)", "com.example.Foo#bar"},
		{"java-method-only", "java", "fib", "fib"}, // no class qualifier: passes through unchanged and will remain unverified at the adapter — Java requires Class#method
		{"java-empty", "java", "", ""},
		{"python-untouched", "python", "mymodule.compute", "mymodule.compute"},
		{"go-untouched", "go", "main.Compute", "main.Compute"},
		{"unknown-lang-untouched", "rust", "foo::bar", "foo::bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFuncBPName(tc.lang, tc.in); got != tc.want {
				t.Fatalf("normalizeFuncBPName(%q, %q) = %q; want %q", tc.lang, tc.in, got, tc.want)
			}
		})
	}
}
