package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Murilovisque/logs"
)

var (
	cache sync.Map
	targetHost string
	port int
	isSLL bool
	httpProtocol string = "http"
	printRequest bool
	printTargetResponse bool
	cacheTimeout = 5
)

func init() {
	flag.StringVar(&targetHost, "target", "", "target host")
	flag.IntVar(&port, "port", -1, "bind port")
	flag.BoolVar(&isSLL, "use-ssl", false, "use-ssl")
	flag.BoolVar(&printRequest, "log-origin-request", false, "Log origin request")
	flag.BoolVar(&printTargetResponse, "log-target-response", false, "Log target response")
	flag.IntVar(&cacheTimeout, "cache-timeout", 5, "Cache timeout")
	flag.Parse()
}

func main() {
	err := validParams()
	if err != nil {
		fmt.Println(err)
		flag.PrintDefaults()
		os.Exit(2)
	}
	rand.Seed(time.Now().UnixNano())
	if isSLL {
		httpProtocol = "https"
	}
	http.HandleFunc("/", serveReverseProxy)
	log.Println("Starting proxy with cache", cacheTimeout, "minute(s)")
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func validParams() error {
	if port < 1 {
		return fmt.Errorf("Invalid port %d", port)
	}
	if cacheTimeout < 1 {
		return fmt.Errorf("Invalid cache-timeout %d", cacheTimeout)
	}
	return nil
}

func readRequest(logger *logs.Logger, req *http.Request) error {
	if !printRequest || req.Method == http.MethodGet {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	bodyRead, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}
	logger.Infof("Request's body %s", string(bodyRead))
	return nil
}

func serveReverseProxy(res http.ResponseWriter, req *http.Request) {
	logger := logs.NewLogger(logs.FieldValue{Key: "reqID", Val: strconv.Itoa(rand.Intn(100000))})
	logger.Infof("Request received %s", req.URL.Path)
	cacheKey := req.URL.Path
	if found, ok := cache.Load(cacheKey); ok {
		resWrapper := found.(httpResponseWriterWrapper)
		res.WriteHeader(resWrapper.resStatusCode)
		for k, vs := range resWrapper.headers {
			for _, v := range vs {
				res.Header().Add(k, v)
			}
		}
		fmt.Fprint(res, resWrapper.remoteResponse.String())
		logger.Info("Return response from cache")
		return
	}
	url, err := url.Parse(fmt.Sprintf("%s://%s", httpProtocol, targetHost))
	if err != nil {
		setInternalErrorResponse(logger, res, err)
	} else if err = readRequest(logger, req); err != nil {
		setInternalErrorResponse(logger, res, err)
	} else {
		proxy := httputil.NewSingleHostReverseProxy(url)
		req.URL.Host = url.Host
		req.URL.Scheme = url.Scheme
		req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
		req.Host = url.Host
		logger.Info("Making proxy")
		resWrapper := httpResponseWriterWrapper{res, strings.Builder{}, res.Header(), 0}
		proxy.ServeHTTP(&resWrapper, req)
		if printTargetResponse {
			logger.Info(resWrapper.String())
		}
		if req.Method == http.MethodGet && resWrapper.resStatusCode >= 200 && resWrapper.resStatusCode < 300 {
			cache.Store(cacheKey, resWrapper)
			time.AfterFunc(time.Duration(cacheTimeout)*time.Minute, func() {
				cache.Delete(cacheKey)
			})
		}
	}
}

type errUnknownHTTPResponse struct {
	resStatus int
	resBody   string
}

func (e errUnknownHTTPResponse) Error() string {
	return fmt.Sprintf("Unknown HTTP Response: Status %d - Body %s\n", e.resStatus, e.resBody)
}

func newErrUnknownHTTPResponse(res *http.Response) error {
	byteBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	return &errUnknownHTTPResponse{resStatus: res.StatusCode, resBody: string(byteBody)}
}

func setInternalErrorResponse(logger *logs.Logger, res http.ResponseWriter, err error) {
	logger.Error(err)
	res.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintln(res, "Erro interno")
}

type httpResponseWriterWrapper struct {
	http.ResponseWriter
	remoteResponse strings.Builder
	headers http.Header
	resStatusCode int
}

func (r *httpResponseWriterWrapper) Write(b []byte) (int, error) {
	r.remoteResponse.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *httpResponseWriterWrapper) WriteHeader(statusCode int) {
	r.resStatusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *httpResponseWriterWrapper) String() string {
	return fmt.Sprintf("{Target-response: Status %d - Header %v - Body %s}", r.resStatusCode, r.headers, r.remoteResponse.String())
}
