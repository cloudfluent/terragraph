package exec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// processGroupRunning reads Linux process states because kill(pgid, 0) also sees zombies whose new parent may never reap them.
func processGroupRunning(pgid int) (bool, error) {
	if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
		return false, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		// The parenthesized command name may contain spaces and closing parentheses, so only the last ')' precedes the fixed stat fields.
		end := strings.LastIndexByte(string(data), ')')
		fields := strings.Fields(string(data)[end+1:])
		if end < 0 || len(fields) < 3 {
			return false, fmt.Errorf("reading process %s: malformed stat", entry.Name())
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil {
			return false, fmt.Errorf("reading process %s group: %w", entry.Name(), err)
		}
		if group == pgid && fields[0] != "Z" && fields[0] != "X" {
			return true, nil
		}
	}
	return false, nil
}
