package httpext

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

var (
	defaultRetryWaitMin = 50 * time.Millisecond
	defaultRetryWaitMax = 200 * time.Millisecond
	defaultRetriesMax   = 5
)

type Client struct {
	HTTPClient   *http.Client
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	RetriesMax   int

	CheckForRetry CheckForRetry
	Backoff       Backoff
}

func NewClient(client *http.Client) *Client {
	return &Client{
		HTTPClient:    client,
		RetryWaitMin:  defaultRetryWaitMin,
		RetryWaitMax:  defaultRetryWaitMax,
		RetriesMax:    defaultRetriesMax,
		CheckForRetry: DefaultRetryPolicy,
		Backoff:       DefaultBackoff,
	}
}

type CheckForRetry func(resp *http.Response, err error) (bool, error)

func DefaultRetryPolicy(resp *http.Response, err error) (bool, error) {
	if err != nil {
		return true, err
	}

	if resp.StatusCode == 0 || resp.StatusCode >= 500 {
		return true, nil
	}

	return false, nil
}

type Backoff func(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration

func DefaultBackoff(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
	sleep := 1 << attemptNum * min
	if sleep > max {
		sleep = max
	}
	return sleep
}

type Request struct {
	body io.ReadSeeker
	*http.Request
}

func NewRequest(method, url string, body io.ReadSeeker) (*Request, error) {
	httpReq, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	return &Request{
		body:    body,
		Request: httpReq,
	}, nil
}

func (c *Client) Do(req *Request) (*http.Response, error) {
	for i := 0; ; i++ {
		if req.body != nil {
			if _, err := req.body.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}

		resp, err := c.HTTPClient.Do(req.Request)

		needRetry, checkErr := c.CheckForRetry(resp, err)
		if !needRetry {
			if checkErr != nil {
				err = checkErr
			}
			return resp, err
		}

		if err == nil {
			c.drainBody(resp.Body)
		}

		if remain := c.RetriesMax - i; remain == 0 {
			break
		}

		wait := c.Backoff(c.RetryWaitMin, c.RetryWaitMax, i, resp)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("%s %s giving up after %d attempts", req.Method, req.URL, c.RetriesMax)
}

const respReadLimit = 1 << 20 // 1 Мб

func (c *Client) drainBody(body io.ReadCloser) {
	defer body.Close()
	_, _ = io.Copy(ioutil.Discard, io.LimitReader(body, respReadLimit))
}

func (c *Client) Get(url string) (*http.Response, error) {
	req, err := NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func (c *Client) Post(url, contentType string, body io.ReadSeeker) (*http.Response, error) {
	req, err := NewRequest("Post", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}
