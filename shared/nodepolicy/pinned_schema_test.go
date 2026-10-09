package nodepolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Read only the pinned source, without importing/constructing network adapters.
// A field accepted by our API must not be silently ignored by the real Core.
func TestPolicyFieldsExistInPinnedCore(t *testing.T) {
	files, err := filepath.Glob("../../native/third_party/Clash.Meta/adapter/outbound/*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("initialize the pinned Clash.Meta submodule before policy compatibility tests")
	}
	types := map[string]*ast.StructType{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range group.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if structure, ok := typeSpec.Type.(*ast.StructType); ok {
					types[typeSpec.Name.Name] = structure
				}
			}
		}
	}
	options := map[string]string{
		"ss": "ShadowSocksOption", "ssr": "ShadowSocksROption", "http": "HttpOption", "socks5": "Socks5Option",
		"vmess": "VmessOption", "vless": "VlessOption", "trojan": "TrojanOption", "snell": "SnellOption",
		"hysteria": "HysteriaOption", "hysteria2": "Hysteria2Option", "tuic": "TuicOption", "wireguard": "WireGuardOption",
		"anytls": "AnyTLSOption", "ssh": "SshOption", "mieru": "MieruOption",
	}
	for protocol, fields := range protocols {
		typeName := options[protocol]
		if types[typeName] == nil {
			t.Fatalf("missing reviewed Core option struct for %s", protocol)
		}
		known := map[string]bool{}
		var walk func(string)
		walk = func(name string) {
			structure := types[name]
			if structure == nil {
				return
			}
			for _, field := range structure.Fields.List {
				if len(field.Names) == 0 {
					if embedded, ok := field.Type.(*ast.Ident); ok {
						walk(embedded.Name)
					}
				}
				if field.Tag == nil {
					continue
				}
				text, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatal(err)
				}
				key := strings.Split(reflect.StructTag(text).Get("proxy"), ",")[0]
				if key != "" && key != "-" {
					known[key] = true
				}
			}
		}
		walk(typeName)
		for field := range fields {
			// Parsed generically by adapter.ParseProxy after creating the outbound.
			if field != "smux" && !known[field] {
				t.Errorf("%s: Core %s does not consume %s", protocol, typeName, field)
			}
		}
	}
}
