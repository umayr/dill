//go:build linux || darwin

package coordination

import "testing"

func TestRuntimePathAdmission(t *testing.T) {
	p := rootlessProfile()
	root := "/private/runtime"
	file := RuntimeFile{Path: root + "/generation/file", UID: 100, GID: 100, Mode: 0400}
	if validateRuntimeFiles(p, root, []RuntimeFile{file}) != nil || validateRuntimeFiles(p, root, nil) != nil {
		t.Fatal("mapped file/empty retirement refused")
	}
	for _, scenario := range []string{"uid", "gid", "outside", "alias", "mode", "negative", "reserved", "root"} {
		bad := file
		r := root
		switch scenario {
		case "uid":
			bad.UID = 65537
		case "gid":
			bad.GID = 65537
		case "outside":
			bad.Path = "/private/runtime-other/file"
		case "alias":
			bad.Path = root + "/generation/../file"
		case "mode":
			bad.Mode = 0600
		case "negative":
			bad.UID = -1
		case "reserved":
			bad.GID = int64(maxID)
		case "root":
			r = "/"
		}
		if validateRuntimeFiles(p, r, []RuntimeFile{bad}) == nil {
			t.Fatal("invalid runtime path/owner admitted", scenario)
		}
	}
	target := &Target{}
	if target.CheckRuntime(root, nil) == nil {
		t.Fatal("metadata without qualification authorized paths")
	}
}
func TestRuntimeViewRequiresSameMountNamespaceAndRoot(t *testing.T) {
	a := runtimeView{Mount: NamespaceID{1, 2}, Root: NamespaceID{3, 4}}
	if !sameRuntimeView(a, a) {
		t.Fatal("matching view refused")
	}
	b := a
	b.Mount.Inode++
	if sameRuntimeView(a, b) {
		t.Fatal("same paths but different mount namespace admitted")
	}
	b = a
	b.Root.Inode++
	if sameRuntimeView(a, b) {
		t.Fatal("different filesystem root admitted")
	}
	if sameRuntimeView(runtimeView{}, runtimeView{}) {
		t.Fatal("missing view admitted")
	}
}
