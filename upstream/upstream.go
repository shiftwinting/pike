package upstream

import (
	"hash/fnv"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vicanso/cod"
	"github.com/vicanso/hes"
	"github.com/vicanso/pike/util"
	up "github.com/vicanso/upstream"

	"github.com/go-yaml/yaml"
	"github.com/vicanso/pike/df"

	proxy "github.com/vicanso/cod-proxy"
)

var (
	upstreams          Upstreams
	errNoMatchUpstream = &hes.Error{
		StatusCode: http.StatusInternalServerError,
		Category:   df.APP,
		Message:    "no match upstream",
		Exception:  true,
	}
	errNoAvailableUpstream = &hes.Error{
		StatusCode: http.StatusInternalServerError,
		Category:   df.APP,
		Message:    "no available upstream",
		Exception:  true,
	}
)

const (
	// backupTag backup server tag
	backupTag = "|backup"

	policyFirst      = "first"
	policyRandom     = "random"
	policyRoundRobin = "roundRobin"
	policyLeastconn  = "leastconn"
	policyIPHash     = "ipHash"
	headerHashPrefix = "header:"
	cookieHashPrefix = "cookie:"
)

type (
	// Backend backend config
	Backend struct {
		Name          string
		Policy        string
		Ping          string
		RequestHeader []string `yaml:"requestHeader"`
		Header        []string
		Prefixs       []string
		Hosts         []string
		Backends      []string
		Rewrites      []string
	}
	// Upstream Upstream
	Upstream struct {
		Policy        string
		Priority      int
		Name          string
		Header        http.Header
		RequestHeader http.Header
		server        up.HTTP
		Hosts         []string
		Prefixs       []string
		Rewrites      []string
		Handler       cod.Handler
	}
	// Upstreams upstream list
	Upstreams []*Upstream
)

func (s Upstreams) Len() int {
	return len(s)
}

func (s Upstreams) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s Upstreams) Less(i, j int) bool {
	return s[i].Priority < s[j].Priority
}

func init() {
	backendConfig := &struct {
		Director []Backend
	}{
		make([]Backend, 0),
	}
	for _, path := range df.ConfigPathList {
		file := filepath.Join(path, "backends.yml")
		buf, _ := ioutil.ReadFile(file)
		if len(buf) != 0 {
			err := yaml.Unmarshal(buf, backendConfig)
			if err != nil {
				break
			}
		}
	}
	upstreams = make(Upstreams, len(backendConfig.Director))
	for index, item := range backendConfig.Director {
		up := NewUpstream(item)
		upstreams[index] = up
	}
	sort.Sort(upstreams)
}

// Proxy do match up stream proxy
func Proxy(c *cod.Context) (err error) {
	var found *Upstream
	for _, item := range upstreams {
		if item.Match(c) {
			found = item
			break
		}
	}
	if found == nil {
		return errNoMatchUpstream
	}
	return found.Handler(c)
}

// hash calculates a hash based on string s
func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// createProxyHandler create proxy handler
func createProxyHandler(us *Upstream) cod.Handler {
	isHeaderPolicy := false
	isCookiePolicy := false
	key := ""
	policy := us.Policy
	if strings.HasPrefix(policy, headerHashPrefix) {
		key = policy[len(headerHashPrefix):]
		isHeaderPolicy = true
	} else if strings.HasPrefix(policy, cookieHashPrefix) {
		key = policy[len(cookieHashPrefix):]
		isCookiePolicy = true
	}
	server := &us.server
	fn := func(c *cod.Context) (*url.URL, error) {
		var result *up.HTTPUpstream
		switch policy {
		case policyFirst:
			result = server.PolicyFirst()
		case policyRandom:
			result = server.PolicyRandom()
		case policyRoundRobin:
			result = server.PolicyRoundRobin()
		case policyLeastconn:
			result = server.PolicyLeastconn()
			// 连接数+1
			result.Inc()
			// 设置 callback
			c.Set(df.ProxyDoneCallback, result.Dec)
		case policyIPHash:
			result = server.GetAvailableUpstream(hash(c.RealIP()))
		default:
			var index uint32
			if isHeaderPolicy {
				index = hash(c.GetRequestHeader(key))
			} else if isCookiePolicy {
				cookie, _ := c.Cookie(key)
				if cookie != nil {
					index = hash(cookie.Value)
				}
			}
			result = server.GetAvailableUpstream(index)
		}
		if result == nil {
			return nil, errNoAvailableUpstream
		}
		return result.URL, nil
	}

	cfg := proxy.Config{
		TargetPicker: fn,
	}
	if len(us.Rewrites) != 0 {
		cfg.Rewrites = us.Rewrites
	}
	return proxy.New(cfg)
}

func createUpstreamFromBackend(backend Backend) *Upstream {
	priority := 8
	if len(backend.Hosts) != 0 {
		priority -= 4
	}
	if len(backend.Prefixs) != 0 {
		priority -= 2
	}
	uh := up.HTTP{
		// use http request check
		Ping: backend.Ping,
	}
	for _, item := range backend.Backends {
		backup := false
		if strings.Contains(item, backupTag) {
			item = strings.Replace(item, backupTag, "", 1)
			backup = true
		}
		item = strings.TrimSpace(item)
		if backup {
			uh.AddBackup(item)
		} else {
			uh.Add(item)
		}
	}

	us := Upstream{
		Policy:   backend.Policy,
		Name:     backend.Name,
		server:   uh,
		Prefixs:  backend.Prefixs,
		Hosts:    backend.Hosts,
		Rewrites: backend.Rewrites,
		Priority: priority,
	}
	// 默认使用 round robin算法
	if us.Policy == "" {
		us.Policy = policyRoundRobin
	}

	h := util.ConvertToHTTPHeader(backend.Header)
	if h != nil {
		us.Header = h
	}
	rh := util.ConvertToHTTPHeader(backend.RequestHeader)
	if rh != nil {
		us.RequestHeader = rh
	}
	return &us
}

// NewUpstream new upstream
func NewUpstream(backend Backend) *Upstream {
	us := createUpstreamFromBackend(backend)
	server := &us.server
	us.Handler = createProxyHandler(us)
	server.DoHealthCheck()
	go server.StartHealthCheck()
	return us
}

// Match match
func (us *Upstream) Match(c *cod.Context) bool {
	hosts := us.Hosts
	if len(hosts) != 0 {
		found := false
		currentHost := c.Request.Host
		for _, host := range hosts {
			if currentHost == host {
				found = true
				break
			}
		}
		// 不匹配host
		if !found {
			return false
		}
	}

	prefixs := us.Prefixs
	if len(prefixs) != 0 {
		found := false
		url := c.Request.RequestURI
		for _, prefix := range prefixs {
			if strings.HasPrefix(url, prefix) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}