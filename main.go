//go:build linux
// +build linux

package main

import (
	"container/list"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerd/containerd/pkg/userns"
	"github.com/docker/docker/pkg/system"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func pathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return false
}

func copyXattr(srcPath, dstPath, attr string) error {
	data, err := system.Lgetxattr(srcPath, attr)
	if err != nil {
		return err
	}
	if data != nil {
		if err := system.Lsetxattr(dstPath, attr, data, 0); err != nil {
			return err
		}
	}
	return nil
}

type fileID struct {
	dev uint64
	ino uint64
}

type dirMtimeInfo struct {
	dstPath *string
	stat    *syscall.Stat_t
}

func DirCopy(srcDir, dstDir string) error {
	// This is a map of source file inodes to dst file paths
	copiedFiles := make(map[fileID]string)
	dirsToSetMtimes := list.New()
	err := filepath.Walk(srcDir, func(srcPath string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, relPath)
		stat, ok := f.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("unable to get raw syscall.Stat_t data for %s", srcPath)
		}

		isHardlink := false
		switch mode := f.Mode(); {
		case mode.IsRegular():
			// the type is 32bit on mips
			id := fileID{dev: uint64(stat.Dev), ino: stat.Ino} //nolint: unconvert
			isHardlink = true
			if !pathExists(dstPath) {
				if err := os.Link(srcPath, dstPath); err != nil {
					return err
				}
			}
			copiedFiles[id] = dstPath
		case mode.IsDir():
			if err := os.Mkdir(dstPath, f.Mode()); err != nil && !os.IsExist(err) {
				return err
			}
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}

			if err := os.Symlink(link, dstPath); err != nil {
				return err
			}
		case mode&os.ModeNamedPipe != 0:
			fallthrough
		case mode&os.ModeSocket != 0:
			if err := unix.Mkfifo(dstPath, uint32(stat.Mode)); err != nil {
				return err
			}

		case mode&os.ModeDevice != 0:
			if userns.RunningInUserNS() {
				// cannot create a device if running in user namespace
				return nil
			}
			if err := unix.Mknod(dstPath, uint32(stat.Mode), int(stat.Rdev)); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unknown file type (%d / %s) for %s", f.Mode(), f.Mode().String(), srcPath)
		}
		if isHardlink {
			return nil
		}
		if err := os.Lchown(dstPath, int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
		if err := copyXattr(srcPath, dstPath, "security.capability"); err != nil {
			return err
		}
		isSymlink := f.Mode()&os.ModeSymlink != 0
		if !isSymlink {
			if err := os.Chmod(dstPath, f.Mode()); err != nil {
				return err
			}
		}
		if f.IsDir() {
			dirsToSetMtimes.PushFront(&dirMtimeInfo{dstPath: &dstPath, stat: stat})
		} else if !isSymlink {
			aTime := time.Unix(stat.Atim.Unix())
			mTime := time.Unix(stat.Mtim.Unix())
			if err := system.Chtimes(dstPath, aTime, mTime); err != nil {
				return err
			}
		} else {
			ts := []syscall.Timespec{stat.Atim, stat.Mtim}
			if err := system.LUtimesNano(dstPath, ts); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for e := dirsToSetMtimes.Front(); e != nil; e = e.Next() {
		mtimeInfo := e.Value.(*dirMtimeInfo)
		ts := []syscall.Timespec{mtimeInfo.stat.Atim, mtimeInfo.stat.Mtim}
		if err := system.LUtimesNano(*mtimeInfo.dstPath, ts); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceQuote:      true,                  //键值对加引号
		TimestampFormat: "2006-01-02 15:04:05", //时间格式
		FullTimestamp:   true,
	})
	sourceTarget := os.Getenv("source_target")
	distTarget := os.Getenv("dist_target")
	if sourceTarget == "" && distTarget == "" {
		logrus.WithFields(logrus.Fields{
			"source_target": sourceTarget,
			"dist_target":   distTarget,
		}).Error("invalid parameter")
		return
	}
	err := DirCopy(sourceTarget, distTarget)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"source_target": sourceTarget,
			"dist_target":   distTarget,
		}).Error(err.Error())
		return
	}
	logrus.WithFields(logrus.Fields{
		"source_target": sourceTarget,
		"dist_target":   distTarget,
	}).Info("copy finish")

}
