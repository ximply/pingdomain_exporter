package main

import (
	"flag"
	"net"
	"os"
	"net/http"
	"strings"
	"regexp"
	"time"
	"fmt"
	"github.com/robfig/cron"
	"io"
	"io/ioutil"
	"context"
	"sync"
)

var (
	Name           = "pingdomain_exporter"
	listenAddress  = flag.String("unix-sock", "/dev/shm/pingdomain_exporter.sock", "Address to listen on for unix sock access and telemetry.")
	metricsPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	dest           = flag.String("dest", "", "Destination list to ping, multi split with ,.")
)

type UnixResponse struct {
	Rsp string
	Status string
}

var lock sync.RWMutex
var g_ret string
var destList []string
var doing bool
var first bool
var now time.Time

func isDomain(domain string) bool {
	b, _ := regexp.MatchString(`[a-zA-Z0-9][-a-zA-Z0-9]{0,62}(.[a-zA-Z0-9][-a-zA-Z0-9]{0,62})+.?`, domain)
	return b
}

func metricsFromUnixSock(unixSockFile string, metricsPath string, timeout time.Duration) UnixResponse {
	rsp := UnixResponse{
		Rsp: "",
		Status: "500",
	}

	c := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", unixSockFile)
			},
		},
		Timeout: timeout,
	}
	res, err := c.Get(fmt.Sprintf("http://unix/%s", metricsPath))
	defer res.Body.Close()
	if err != nil {
		return rsp
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return rsp
	}
	rsp.Rsp = string(body)
	rsp.Status = "200"
	return rsp
}

func doWork() {
	if first {
		if time.Now().Unix() - now.Unix() < 60 {
			return
		}
		first = false
	}

	if doing {
		return
	}
	doing = true

	ret := ""
	for _, i := range destList {
		rsp := metricsFromUnixSock(fmt.Sprintf("/dev/shm/ping_exporter.%s.sock", i),
			"metrics", time.Second)
		if strings.Compare(rsp.Status, "200") != 0 {
			continue
		}
		ret = ret + rsp.Rsp + "\n"
	}

	lock.Lock()
	g_ret = ret
	lock.Unlock()

	doing = false
}

func metrics(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	ret := g_ret
	lock.RUnlock()

	io.WriteString(w, ret)
}

func main() {
	flag.Parse()

	addr := "/dev/shm/pingdomain_exporter.sock"
	if listenAddress != nil {
		addr = *listenAddress
	}

	if dest == nil || len(*dest) == 0 {
		panic("error dest")
	}
	l := strings.Split(*dest, ",")
	for _, i := range l {
		if isDomain(i) {
			destList = append(destList, i)
		}
	}

	if len(destList) == 0 {
		panic("no one to ping")
	}

	doing = false
	first = true
	now = time.Now()
	//doWork()
	c := cron.New()
	c.AddFunc("0 */1 * * * ?", doWork)
	c.Start()

	mux := http.NewServeMux()
	mux.HandleFunc(*metricsPath, metrics)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Ping Domain Exporter</title></head>
             <body>
             <h1>Ping Domain Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	server := http.Server{
		Handler: mux, // http.DefaultServeMux,
	}
	os.Remove(addr)

	listener, err := net.Listen("unix", addr)
	if err != nil {
		panic(err)
	}
	server.Serve(listener)
}