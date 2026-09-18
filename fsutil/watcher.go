package fsutil

import (
	"fmt"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

func WatchService(path string, fileChannel chan string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(path)
	if err != nil {
		return err
	}

	//loop:
	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create == fsnotify.Create {
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}
				if info.IsDir() {
					continue
				}
				//event.Name이 파일 들어온 경로
				err = WaitFileReady(event.Name, 10*time.Second) //파일이 완전히 쓰여질 때까지 대기
				if err != nil {
					continue
				}
				fileChannel <- event.Name
			}
		}
	}

	return nil
}

func WaitFileReady(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var prevSize int64 = -1

	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		size := info.Size()
		if size > 0 && size == prevSize {
			return nil // 크기 변화 없음 -> 쓰기 완료
		}
		prevSize = size
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for file ready: %s", path)
}

func EnsureDir(dirs ...string) error {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("error creating directory %s: %w", dir, err)
		}
	}
	return nil
}
