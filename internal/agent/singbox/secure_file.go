package singbox

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeSecretFile atomically replaces a file containing credentials. CreateTemp
// uses 0600, and the final chmod also fixes permissions on files created by older
// releases.
func writeSecretFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".nodectl-secret-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("原子替换文件失败: %w", err)
	}
	return os.Chmod(path, 0600)
}
