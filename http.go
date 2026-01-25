package cerberus

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	ErrNoRedirect  = errors.New("No redirect to challenge page.")
	ErrParseFailed = errors.New("Failed to parse challenge from HTML data tags.")
)

func NewChallenge(hc http.Client, host string) (Challenge, error) {
	u, err := parseHost(host)
	if err != nil {
		return Challenge{}, err
	}

	// Update host url in case we get redirected across domains.
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		rh := req.URL.Host
		if rh != u.Host && strings.HasPrefix(rh, "kiwifarms") {
			u.Host = rh
		}

		return nil
	}

	resp, err := hc.Get(u.String())
	if err != nil {
		return Challenge{}, err
	}
	defer resp.Body.Close()

	// Check for 203 status
	if resp.StatusCode != 203 {
		return Challenge{}, ErrNoRedirect
	}

	// Kept separate from the return because of the defer.
	c, err := parseTags(resp.Body)
	if err != nil {
		return Challenge{}, err
	}
	c.host = u

	return c, nil
}

func Submit(hc http.Client, s Solution) (string, error) {
	resp, err := postSolution(hc, s)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	return resp.Header.Get("Set-Cookie"), nil
}

func postSolution(hc http.Client, s Solution) (*http.Response, error) {
	// Ensure the POST url parses properly before passing the string.
	u, err := url.Parse(fmt.Sprintf("%s://%s/.ttrs/challenge", s.host.Scheme, s.host.Hostname()))
	if err != nil {
		return nil, err
	}

	return hc.PostForm(u.String(), url.Values{
		"salt":     []string{s.Salt},
		"redirect": []string{s.Redirect},
		"nonce":    []string{fmt.Sprint(s.Nonce)},
	})
}

func parseHost(addr string) (*url.URL, error) {
	// Guess https as protocol if one wasn't provided and hope it parses.
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	return u, nil
}

func parseTags(r io.Reader) (Challenge, error) {
	c := Challenge{}

	z := html.NewTokenizer(r)
	for i := z.Next(); i != html.ErrorToken; i = z.Next() {
		tk := z.Token()
		if tk.DataAtom == atom.Html {
			for _, a := range tk.Attr {
				switch a.Key {
				case "data-ttrs-challenge":
					c.Salt = a.Val
				case "data-ttrs-difficulty":
					diff, err := strconv.Atoi(a.Val)
					if err != nil {
						return c, ErrParseFailed
					}
					c.Diff = uint32(diff)
				case "data-ttrs-steps":
					steps, err := strconv.Atoi(a.Val)
					if err != nil {
						return c, ErrParseFailed
					}
					c.Steps = uint32(steps)
				}
			}
		}
	}

	if c.Salt == "" {
		return c, ErrParseFailed
	}

	return c, nil
}
