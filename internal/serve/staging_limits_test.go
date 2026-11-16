package serve

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/projectsnapshot"
)

func TestStagingConfiguredLimitsBoundaries(t *testing.T) {
	ctx := context.Background()
	data := make([]byte, 4<<20)
	sum := sha256.Sum256(data)
	files := make([]projectsnapshot.Entry, 4)
	for i := range files {
		files[i] = projectsnapshot.Entry{Path: fmt.Sprintf("%d", i), Content: data, SHA256: sum}
	}
	s := projectsnapshot.Snapshot{Entrypoint: "0", Files: files}
	if err := projectsnapshot.Validate(ctx, s, stageLimits); err != nil {
		t.Fatal(err)
	}
	tooBig := make([]byte, (4<<20)+1)
	s.Files = []projectsnapshot.Entry{{Path: "0", Content: tooBig, SHA256: sha256.Sum256(tooBig)}}
	if projectsnapshot.Validate(ctx, s, stageLimits) == nil {
		t.Fatal("file limit")
	}
	s.Files = append(files, projectsnapshot.Entry{Path: "extra", Content: []byte{1}, SHA256: sha256.Sum256([]byte{1})})
	if projectsnapshot.Validate(ctx, s, stageLimits) == nil {
		t.Fatal("total limit")
	}
	s.Files = make([]projectsnapshot.Entry, 1000)
	for i := range s.Files {
		s.Files[i] = projectsnapshot.Entry{Path: fmt.Sprintf("%d", i), SHA256: sha256.Sum256(nil)}
	}
	if err := projectsnapshot.Validate(ctx, s, stageLimits); err != nil {
		t.Fatal(err)
	}
	s.Files = append(s.Files, projectsnapshot.Entry{Path: "extra", SHA256: sha256.Sum256(nil)})
	if projectsnapshot.Validate(ctx, s, stageLimits) == nil {
		t.Fatal("count limit")
	}
	dirs := make([]projectsnapshot.Entry, 254)
	for i := range dirs {
		dirs[i] = projectsnapshot.Entry{Path: fmt.Sprintf("d%d/a", i), SHA256: sha256.Sum256(nil)}
	}
	s = projectsnapshot.Snapshot{Entrypoint: "d0/a", Files: dirs}
	if err := projectsnapshot.Validate(ctx, s, stageLimits); err != nil {
		t.Fatal(err)
	}
	s.Files = append(s.Files, projectsnapshot.Entry{Path: "extra/a", SHA256: sha256.Sum256(nil)})
	if projectsnapshot.Validate(ctx, s, stageLimits) == nil {
		t.Fatal("directory limit")
	}
	path512 := strings.Repeat("a", 255) + "/" + strings.Repeat("b", 254) + "/x"
	s = projectsnapshot.Snapshot{Entrypoint: path512, Files: []projectsnapshot.Entry{{Path: path512, SHA256: sha256.Sum256(nil)}}}
	if err := projectsnapshot.Validate(ctx, s, stageLimits); err != nil {
		t.Fatal(err)
	}
	path513 := path512 + "x"
	s.Entrypoint = path513
	s.Files[0].Path = path513
	if projectsnapshot.Validate(ctx, s, stageLimits) == nil {
		t.Fatal("path limit")
	}
	path := strings.Repeat("a/", 32) + "a"
	s = projectsnapshot.Snapshot{Entrypoint: path, Files: []projectsnapshot.Entry{{Path: path, SHA256: sha256.Sum256(nil)}}}
	if projectsnapshot.Validate(ctx, s, stageLimits) == nil {
		t.Fatal("depth limit")
	}
}
