package proxy

import (
	"hash/fnv"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo"
	"github.com/vicanso/dash"
)

type (
	// Director 服务器列表
	Director struct {
		// 名称
		Name string
		// backend的选择策略
		Policy string
		// ping设置（检测backend是否要用）
		Ping string
		// backend列表
		Backends []string
		// 可用的backend列表（通过ping检测）
		availableBackends []string
		// host列表
		Hosts []string
		// url前缀
		Prefixs []string
		// 优先级
		Priority int
		// 读写锁
		mutex sync.RWMutex
		// roubin 的次数
		roubin uint32
	}
	// Directors 用于director排序
	Directors []*Director
	// SelectFunc 用于选择排序的方法
	SelectFunc func(echo.Context, *Director) uint32
)

var selectFuncMap = make(map[string]SelectFunc)

// 保证director列表
var directorList = make(Directors, 0)

// hash calculates a hash based on string s
func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// AddSelect 增加select的处理函数
func AddSelect(name string, fn SelectFunc) {
	selectFuncMap[name] = fn
}

func init() {
	AddSelect("first", func(c echo.Context, d *Director) uint32 {
		return 0
	})
	AddSelect("random", func(c echo.Context, d *Director) uint32 {
		return rand.Uint32()
	})
	AddSelect("roundRobin", func(c echo.Context, d *Director) uint32 {
		return atomic.AddUint32(&d.roubin, 1)
	})
	AddSelect("ipHash", func(c echo.Context, d *Director) uint32 {
		return hash(c.RealIP())
	})
}

// Len 获取director slice的长度
func (s Directors) Len() int {
	return len(s)
}

// Swap 元素互换
func (s Directors) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s Directors) Less(i, j int) bool {
	return s[i].Priority < s[j].Priority
}

// RefreshPriority 刷新优先级计算
func (d *Director) RefreshPriority() {
	priority := 8
	// 如果有配置host，优先前提升4
	if len(d.Hosts) != 0 {
		priority -= 4
	}
	// 如果有配置prefix，优先级提升2
	if len(d.Prefixs) != 0 {
		priority -= 2
	}
	d.Priority = priority
}

// AddBackend 增加backend
func (d *Director) AddBackend(backend string) {
	backends := d.Backends
	if !dash.IncludesString(backends, backend) {
		d.Backends = append(backends, backend)
	}
}

// RemoveBackend 删除backend
func (d *Director) RemoveBackend(backend string) {
	backends := d.Backends
	index := dash.FindStringIndex(backends, backend)
	if index != -1 {
		d.Backends = append(backends[0:index], backends[index+1:]...)
	}
}

// AddAvailableBackend 增加可用backend列表
func (d *Director) AddAvailableBackend(backend string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	backends := d.availableBackends
	if !dash.IncludesString(backends, backend) {
		d.availableBackends = append(backends, backend)
	}
}

// RemoveAvailableBackend 删除可用的backend
func (d *Director) RemoveAvailableBackend(backend string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	backends := d.availableBackends
	index := dash.FindStringIndex(backends, backend)
	if index != -1 {
		d.availableBackends = append(backends[0:index], backends[index+1:]...)
	}
}

// GetAvailableBackends 获取可用的backend
func (d *Director) GetAvailableBackends() []string {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	return d.availableBackends
}

// AddHost 添加host
func (d *Director) AddHost(host string) {
	hosts := d.Hosts
	if !dash.IncludesString(hosts, host) {
		d.Hosts = append(hosts, host)
		d.RefreshPriority()
	}
}

// RemoveHost 删除host
func (d *Director) RemoveHost(host string) {
	hosts := d.Hosts
	index := dash.FindStringIndex(hosts, host)
	if index != -1 {
		d.Hosts = append(hosts[0:index], hosts[index+1:]...)
		d.RefreshPriority()
	}
}

// AddPrefix 增加前缀
func (d *Director) AddPrefix(prefix string) {
	prefixs := d.Prefixs
	if !dash.IncludesString(prefixs, prefix) {
		d.Prefixs = append(prefixs, prefix)
		d.RefreshPriority()
	}
}

// RemovePrefix 删除前缀
func (d *Director) RemovePrefix(prefix string) {
	prefixs := d.Prefixs
	index := dash.FindStringIndex(prefixs, prefix)
	if index != -1 {
		d.Prefixs = append(prefixs[0:index], prefixs[index+1:]...)
		d.RefreshPriority()
	}
}

// Match 判断是否符合
func (d *Director) Match(host, uri string) (match bool) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	hosts := d.Hosts
	prefixs := d.Prefixs
	// 如果未配置host与prefix，则所有请求都匹配
	if len(hosts) == 0 && len(prefixs) == 0 {
		return true
	}
	// 判断host是否符合
	if len(hosts) != 0 {
		hostBytes := []byte(host)
		for _, item := range hosts {
			if match {
				break
			}
			reg := regexp.MustCompile(item)
			if reg.Match(hostBytes) {
				match = true
			}
		}
		// 如果host不匹配，直接返回
		if !match {
			return
		}
	}

	// 判断prefix是否符合
	if len(prefixs) != 0 {
		// 重置match状态，再判断prefix
		match = false
		for _, item := range prefixs {
			if !match && strings.HasPrefix(uri, item) {
				match = true
			}
		}
	}
	return
}

// 检测url，如果5次有3次通过则认为是healthy
func doCheck(url string) (healthy bool) {
	c := make(chan int)

	go func() {
		for i := 0; i < 5; i++ {
			client := http.Client{
				Timeout: time.Duration(3 * time.Second),
			}
			resp, err := client.Get(url)
			// chan 0表示不通过，1表示通过
			if err != nil {
				c <- 0
			} else {
				statusCode := resp.StatusCode
				if statusCode >= 200 && statusCode < 400 {
					c <- 1
				} else {
					c <- 0
				}
			}
		}
		close(c)
	}()
	successCount := 0
	for i := range c {
		successCount += i
	}
	if successCount >= 3 {
		healthy = true
	}
	return
}

// Select 根据Policy选择一个backend
func (d *Director) Select(c echo.Context) string {
	fn := selectFuncMap[d.Policy]
	if fn == nil {
		return ""
	}
	availableBackends := d.GetAvailableBackends()
	count := uint32(len(availableBackends))

	index := fn(c, d)

	return availableBackends[index%count]

}

// HealthCheck 对director下的服务器做健康检测
func (d *Director) HealthCheck() {
	backends := d.Backends
	for _, item := range backends {
		go func(backend string) {
			url := backend + d.Ping
			healthy := doCheck(url)
			if healthy {
				d.AddAvailableBackend(backend)
			} else {
				d.RemoveAvailableBackend(backend)
			}
		}(item)
	}
}

// StartHealthCheck 启用health check
func (d *Director) StartHealthCheck(interval time.Duration) {
	defer func() {
		if err := recover(); err != nil {
			// 如果异常，等待后继续检测
			// TODO 增加错误日志输出
			time.Sleep(time.Second)
			d.StartHealthCheck(interval)
		}
	}()

	ticker := time.NewTicker(interval)
	for _ = range ticker.C {
		d.HealthCheck()
	}
}