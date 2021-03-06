package gosparkpost

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"

	certifi "github.com/certifi/gocertifi"
	"github.com/pkg/errors"
)

// Config includes all information necessary to make an API request.
type Config struct {
	BaseUrl    string
	ApiKey     string
	Username   string
	Password   string
	ApiVersion int
	Verbose    bool
}

// Client contains connection, configuration, and authentication information.
// Specifying your own http.Client gives you lots of control over how connections are made.
// Clients are safe for concurrent (read-only) reuse by multiple goroutines.
type Client struct {
	Config  *Config
	Client  *http.Client
	headers map[string]string
}

var nonDigit *regexp.Regexp = regexp.MustCompile(`\D`)

// NewConfig builds a Config object using the provided map.
func NewConfig(m map[string]string) (*Config, error) {
	c := &Config{}

	if baseurl, ok := m["baseurl"]; ok {
		c.BaseUrl = baseurl
	} else {
		return nil, errors.New("BaseUrl is required for api config")
	}

	if apikey, ok := m["apikey"]; ok {
		c.ApiKey = apikey
	} else {
		return nil, errors.New("ApiKey is required for api config")
	}

	return c, nil
}

// Response contains information about the last HTTP response.
// Helpful when an error message doesn't necessarily give the complete picture.
// Also contains any messages emitted as a result of the Verbose config option.
type Response struct {
	HTTP    *http.Response
	Body    []byte
	Verbose map[string]string
	Results interface{} `json:"results,omitempty"`
	Errors  []Error     `json:"errors,omitempty"`
}

// Error mirrors the error format returned by SparkPost APIs.
type Error struct {
	Message     string `json:"message"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Part        string `json:"part,omitempty"`
	Line        int    `json:"line,omitempty"`
}

func (e Error) Json() (string, error) {
	jsonBytes, err := json.Marshal(e)
	if err != nil {
		return "", errors.Wrap(err, "marshaling json")
	}
	return string(jsonBytes), nil
}

// Init pulls together everything necessary to make an API request.
// Caller may provide their own http.Client by setting it in the provided API object.
func (api *Client) Init(cfg *Config) error {
	// Set default values
	if cfg.BaseUrl == "" {
		cfg.BaseUrl = "https://api.sparkpost.com"
	} else if !strings.HasPrefix(cfg.BaseUrl, "https://") {
		return errors.New("API base url must be https!")
	}
	if cfg.ApiVersion == 0 {
		cfg.ApiVersion = 1
	}
	api.Config = cfg
	api.headers = make(map[string]string)

	if api.Client == nil {
		// Ran into an issue where USERTrust was not recognized on OSX.
		// The rest of this block was the fix.

		// load Mozilla cert pool
		pool, err := certifi.CACerts()
		if err != nil {
			return errors.Wrap(err, "loading certifi cert pool")
		}

		// configure transport using Mozilla cert pool
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}

		// configure http client using transport
		api.Client = &http.Client{Transport: transport}
	}

	return nil
}

// SetHeader adds additional HTTP headers for every API request made from client.
// Useful to set subaccount X-MSYS-SUBACCOUNT header and etc.
// All calls to SetHeader must happen before Client is exposed to possible concurrent use.
func (c *Client) SetHeader(header string, value string) {
	c.headers[header] = value
}

// RemoveHeader removes a header set in SetHeader function
// All calls to RemoveHeader must happen before Client is exposed to possible concurrent use.
func (c *Client) RemoveHeader(header string) {
	delete(c.headers, header)
}

// HttpPost sends a Post request with the provided JSON payload to the specified url.
// Query params are supported via net/url - roll your own and stringify it.
// Authenticate using the configured API key.
func (c *Client) HttpPost(ctx context.Context, url string, data []byte) (*Response, error) {
	return c.DoRequest(ctx, "POST", url, data)
}

// HttpGet sends a Get request to the specified url.
// Query params are supported via net/url - roll your own and stringify it.
// Authenticate using the configured API key.
func (c *Client) HttpGet(ctx context.Context, url string) (*Response, error) {
	return c.DoRequest(ctx, "GET", url, nil)
}

// HttpPut sends a Put request with the provided JSON payload to the specified url.
// Query params are supported via net/url - roll your own and stringify it.
// Authenticate using the configured API key.
func (c *Client) HttpPut(ctx context.Context, url string, data []byte) (*Response, error) {
	return c.DoRequest(ctx, "PUT", url, data)
}

// HttpDelete sends a Delete request to the provided url.
// Query params are supported via net/url - roll your own and stringify it.
// Authenticate using the configured API key.
func (c *Client) HttpDelete(ctx context.Context, url string) (*Response, error) {
	return c.DoRequest(ctx, "DELETE", url, nil)
}

func (c *Client) DoRequest(ctx context.Context, method, urlStr string, data []byte) (*Response, error) {
	req, err := http.NewRequest(method, urlStr, bytes.NewBuffer(data))
	if err != nil {
		return nil, errors.Wrap(err, "building request")
	}

	ares := &Response{}
	if c.Config.Verbose {
		if ares.Verbose == nil {
			ares.Verbose = map[string]string{}
		}
		ares.Verbose["http_method"] = method
		ares.Verbose["http_uri"] = urlStr
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")

		if c.Config.Verbose {
			ares.Verbose["http_postdata"] = string(data)
		}
	}

	// TODO: set User-Agent based on gosparkpost version and possibly git's short hash
	req.Header.Set("User-Agent", "GoSparkPost v0.1")

	if c.Config.ApiKey != "" {
		req.Header.Set("Authorization", c.Config.ApiKey)
	} else if c.Config.Username != "" {
		req.Header.Add("Authorization", "Basic "+basicAuth(c.Config.Username, c.Config.Password))
	}

	// Forward additional headers set in client to request
	for header, value := range c.headers {
		req.Header.Set(header, value)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	// set any headers provided in context
	if header, ok := ctx.Value("http.Header").(http.Header); ok {
		for key, vals := range map[string][]string(header) {
			if len(vals) >= 1 {
				// replace existing headers, default, or from Client.headers
				req.Header.Set(key, vals[0])
			}
			if len(vals) > 2 {
				for _, val := range vals[1:] {
					// allow setting multiple values because why not
					req.Header.Add(key, val)
				}
			}
		}
	}
	req = req.WithContext(ctx)

	if c.Config.Verbose {
		reqBytes, err := httputil.DumpRequestOut(req, false)
		if err != nil {
			return ares, errors.Wrap(err, "saving request")
		}
		ares.Verbose["http_requestdump"] = string(reqBytes)
	}

	res, err := c.Client.Do(req)
	ares.HTTP = res

	if c.Config.Verbose {
		ares.Verbose["http_status"] = ares.HTTP.Status
		bodyBytes, dumpErr := httputil.DumpResponse(res, true)
		if err != nil {
			ares.Verbose["http_responsedump_err"] = dumpErr.Error()
		} else {
			ares.Verbose["http_responsedump"] = string(bodyBytes)
		}
	}

	if err != nil {
		return ares, errors.Wrap(err, "error response")
	}
	return ares, nil
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// ReadBody is a convenience method that returns the http.Response body.
// The first time this function is called, the body is read from the
// http.Response. For subsequent calls, the cached version in
// Response.Body is returned.
func (r *Response) ReadBody() ([]byte, error) {
	// Calls 2+ to this function for the same http.Response will now DWIM
	if r.Body != nil {
		return r.Body, nil
	}

	defer r.HTTP.Body.Close()
	bodyBytes, err := ioutil.ReadAll(r.HTTP.Body)
	if err != nil {
		return bodyBytes, errors.Wrap(err, "reading http body")
	}
	r.Body = bodyBytes
	return bodyBytes, nil
}

// ParseResponse pulls info from JSON http responses into api.Response object.
// It's helpful to call Response.AssertJson before calling this function.
func (r *Response) ParseResponse() error {
	body, err := r.ReadBody()
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, r)
	if err != nil {
		return errors.Wrap(err, "parsing api response")
	}

	return nil
}

// AssertJson returns an error if the provided HTTP response isn't JSON.
func (r *Response) AssertJson() error {
	if r.HTTP == nil {
		return errors.New("AssertJson got nil http.Response")
	}
	ctype := r.HTTP.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(ctype)
	if err != nil {
		return errors.Wrap(err, "parsing content-type")
	}
	// allow things like "application/json; charset=utf-8" in addition to the bare content type
	if mediaType != "application/json" {
		return fmt.Errorf("Expected json, got [%s] with code %d", mediaType, r.HTTP.StatusCode)
	}
	return nil
}

// PrettyError returns a human-readable error message for common http errors returned by the API.
// The string parameters are used to customize the generated error message
// (example: noun=template, verb=create).
func (r *Response) PrettyError(noun, verb string) error {
	if r.HTTP == nil {
		return nil
	}
	code := r.HTTP.StatusCode
	if code == 404 {
		return fmt.Errorf("%s does not exist, %s failed.", noun, verb)
	} else if code == 401 {
		return fmt.Errorf("%s %s failed, permission denied. Check your API key.", noun, verb)
	} else if code == 403 {
		// This is what happens if an endpoint URL gets typo'd.
		return fmt.Errorf("%s %s failed. Are you using the right API path?", noun, verb)
	}
	return nil
}
