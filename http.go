package cerberus

import (
	"context"
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

type ErrInvalidSolution struct {
	s Solution
}

func (e *ErrInvalidSolution) Error() string {
	return fmt.Sprintf("Received 400 status when submitting solution: %+v", e.s)
}

// Request new Tartarus challenge from provided host.
func NewChallenge(ctx context.Context, hc http.Client, host string) (Challenge, error) {
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

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return Challenge{}, err
	}

	resp, err := hc.Do(req)
	if err != nil {
		return Challenge{}, err
	}
	defer resp.Body.Close()

	// Check for 203 status. A 203 indicates a redirect to a challenge page.
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

// Submit a Solution for a Challenge.
//
// If redirect is empty, "/" is used as a sensible default.
// Auth cookies get set automatically in http.Client's CookieJar.
// The *http.Response is provided to support more advanced setups. The auth token can also be found in its Body.
func Submit(ctx context.Context, hc http.Client, s Solution, redirect string) (*http.Response, error) {
	if redirect != "" {
		s.Redirect = redirect
	}

	resp, err := postSolution(ctx, hc, s)
	if err != nil {
		return nil, err
	}

	if s.Steps > 0 {
		c, err := NewChallenge(ctx, hc, s.host.String())
		if err != nil {
			return nil, err
		}

		// maybe useful later. idk.
		// c.Steps = s.Steps

		s, err := Solve(ctx, c)
		if err != nil {
			return nil, err
		}

		return Submit(ctx, hc, s, redirect)
	}

	return resp, nil
}

func parseHost(addr string) (*url.URL, error) {
	// Guess https as protocol if one wasn't provided and hope it parses.
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}

	return url.Parse(addr)
}

func postSolution(ctx context.Context, hc http.Client, s Solution) (*http.Response, error) {
	// Ensure the POST url parses properly before passing the string.
	u, err := url.Parse(fmt.Sprintf("%s://%s/.ttrs/challenge", s.host.Scheme, s.host.Hostname()))
	if err != nil {
		return nil, err
	}

	reqBody := strings.NewReader(fmt.Sprintf(`salt=%s&redirect=%s&nonce=%d`, s.Salt, url.PathEscape(s.Redirect), s.Nonce))
	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}

	// TODO: Additionally verify failure from response JSON. Maybe include resp body in err type.
	// Rejected solution response: status=400 body={"success":false,"reason":"invalid_solution","action":"retry"}
	if resp.StatusCode == 400 {
		defer resp.Body.Close()
		return nil, &ErrInvalidSolution{s}
	}

	return resp, nil
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
					c.Steps = int8(steps)
				}
			}
		}
	}

	if c.Salt == "" {
		return c, ErrParseFailed
	}

	return c, nil
}
