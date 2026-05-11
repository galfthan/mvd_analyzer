package democache

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRoot returns the conventional on-disk cache root.
// Honors XDG_CACHE_HOME; falls back to ~/.cache/qw-mvd.
func DefaultRoot() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "qw-mvd")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "qw-mvd")
	}
	return filepath.Join(home, ".cache", "qw-mvd")
}

// mvdPath returns the on-disk path for tier-1 (raw MVD gz bytes).
//
//	<root>/mvd/<sha[:2]>/<sha>.mvd.gz
func mvdPath(root, sha string) string {
	return filepath.Join(root, "mvd", sha[:2], sha+".mvd.gz")
}

// resultPath returns the on-disk path for tier-2 (parsed *Result gob),
// keyed by schema version so schema bumps invalidate this tier without
// touching tier-1.
//
//	<root>/results/v<N>/<sha[:2]>/<sha>.gob
func resultPath(root string, schemaVersion int, sha string) string {
	return filepath.Join(root, "results", fmt.Sprintf("v%d", schemaVersion), sha[:2], sha+".gob")
}

// gameIndexPath returns the on-disk path for the gameId → sha map.
//
//	<root>/index/games/<gameId>.txt
func gameIndexPath(root string, gameID int) string {
	return filepath.Join(root, "index", "games", fmt.Sprintf("%d.txt", gameID))
}
