package process

// 进程识别双层缓存（2026-10-07 效率审计：FindByRequest 每次 2 次全系统 TCP 表
// 枚举 + 5 次 syscall，实测串行 ~2.4ms/次，占转发路径每请求成本的大头）。
//
// 层1（连接四元组 → PID）：连接存活期间 PID 不变；同一四元组在 TIME_WAIT 期
// 内不可能被其他进程复用（内核保证），TTL 30s 低于 TIME_WAIT 下限，命中即免
// 全表枚举。
//
// 层2（(PID, 启动时间) → 可执行路径）：路径终身不变，键带启动时间防 PID 复用
// 错配（拿启动时间只需 OpenProcess+GetProcessTimes，远轻于路径查询），命中即
// 免 OpenProcess+QueryFullProcessImageName+GetLongPathName×2。
//
// 两层都有容量上限，超限整体清空——进程/连接集合有界，全清成本极低；失败
// 结果不缓存（连接消失/进程退出是常态，缓存负条目反而放大陈旧性）。

import (
	"net"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	connCacheTTL = 30 * time.Second
	connCacheCap = 8192
	procCacheCap = 4096
	descCacheCap = 4096
)

type connCacheKey struct {
	srcIP   [16]byte
	dstIP   [16]byte
	srcPort uint16
	dstPort uint16
}

type connCacheEntry struct {
	pid    PID
	expiry time.Time
}

var (
	connCacheMu sync.RWMutex
	connCache   = make(map[connCacheKey]connCacheEntry)

	procCacheMu sync.RWMutex
	procCache   = make(map[procCacheKey]string)

	descCacheMu sync.RWMutex
	descCache   = make(map[string]string)
)

type procCacheKey struct {
	pid       uint32
	startTime int64 // Windows FILETIME，防 PID 复用错配
}

func connCacheKeyOf(srcPort, dstPort uint16, srcIP, dstIP net.IP) connCacheKey {
	k := connCacheKey{srcPort: srcPort, dstPort: dstPort}
	copy(k.srcIP[:], srcIP.To16())
	copy(k.dstIP[:], dstIP.To16())
	return k
}

// cachedPIDByIP 是 findPIDByIP 的缓存包装：命中免全表枚举。
func cachedPIDByIP(srcPort, dstPort uint16, srcIP, dstIP net.IP) (PID, error) {
	key := connCacheKeyOf(srcPort, dstPort, srcIP, dstIP)
	now := time.Now()

	connCacheMu.RLock()
	entry, ok := connCache[key]
	connCacheMu.RUnlock()
	if ok && now.Before(entry.expiry) {
		return entry.pid, nil
	}

	pid, err := findPidByPort(srcPort)
	if err != nil {
		return 0, err
	}

	connCacheMu.Lock()
	if len(connCache) >= connCacheCap {
		connCache = make(map[connCacheKey]connCacheEntry, connCacheCap)
	}
	connCache[key] = connCacheEntry{pid: PID(pid), expiry: now.Add(connCacheTTL)}
	connCacheMu.Unlock()
	return PID(pid), nil
}

// cachedProcPath 是 getProcPath 的缓存包装：键含进程启动时间。
func cachedProcPath(pid uint32) (string, error) {
	key, err := procCacheKeyOf(pid)
	if err != nil {
		// 拿不到启动时间（进程刚退出等）就不缓存，直接走原查询并如实报错。
		return getProcPath(pid)
	}

	procCacheMu.RLock()
	path, ok := procCache[key]
	procCacheMu.RUnlock()
	if ok {
		return path, nil
	}

	path, err = getProcPath(pid)
	if err != nil {
		return "", err
	}

	procCacheMu.Lock()
	if len(procCache) >= procCacheCap {
		procCache = make(map[procCacheKey]string, procCacheCap)
	}
	procCache[key] = path
	procCacheMu.Unlock()
	return path, nil
}

func procCacheKeyOf(pid uint32) (procCacheKey, error) {
	proc, err := openProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return procCacheKey{}, err
	}
	defer windows.CloseHandle(proc)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.Handle(proc), &creation, &exit, &kernel, &user); err != nil {
		return procCacheKey{}, err
	}
	return procCacheKey{pid: pid, startTime: creation.Nanoseconds()}, nil
}

// cachedFileDescription 返回 path 的版本资源描述（ok=false 表示需要回退）。
// 同一 exe 的描述终身不变，按路径键缓存；读取失败不缓存（pidName 会回退
// basename），空描述是 exe 的确定属性，同样缓存以免重复 syscall。
func cachedFileDescription(path string) (string, bool) {
	descCacheMu.RLock()
	desc, ok := descCache[path]
	descCacheMu.RUnlock()
	if ok {
		return desc, true
	}
	desc, err := getFileDescription(path)
	if err != nil {
		return "", false
	}
	descCacheMu.Lock()
	if len(descCache) >= descCacheCap {
		descCache = make(map[string]string, descCacheCap)
	}
	descCache[path] = desc
	descCacheMu.Unlock()
	return desc, true
}
