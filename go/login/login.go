// login handles logging in users.
package login

// Theory of operation.
//
// We use OAuth 2.0 handle authentication. We are essentially doing OpenID
// Connect, but vastly simplified since we are hardcoded to Google's endpoints.
//
// We do a simple OAuth 2.0 flow where the user is asked to grant permission to
// the 'email' scope. See https://developers.google.com/+/api/oauth#email for
// details on that scope. Note that you need to have the Google Plus API turned
// on in your project for this to work, but note that the 'email' scope will
// still work for people w/o Google Plus accounts.
//
// Now in theory once we are authorized and have an Access Token we could call
// https://developers.google.com/+/api/openidconnect/getOpenIdConnect and get the
// users email address. But here we can cheat, as we know it's Google and that for
// the 'email' scope an ID Token will be returned along with the Access Token.
// If we decode the ID Token we can get the email address out of that w/o the
// need for the second round trip. This is all clearly *ahem* explained here:
//
//   https://developers.google.com/accounts/docs/OAuth2Login#exchangecode
//
// Once we get the users email address we put it in a cookie for later
// retrieval. The cookie value is validated using HMAC to stop spoofing.
//
// N.B. The cookiesaltkey metadata value must be set on the GCE instance.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.google.com/p/goauth2/oauth"
	"github.com/gorilla/securecookie"
	"github.com/skia-dev/glog"
	"go.skia.org/infra/go/metadata"
	"go.skia.org/infra/go/util"
)

const (
	COOKIE_NAME         = "sktoken"
	DEFAULT_SCOPE       = "email"
	SESSION_COOKIE_NAME = "sksession"
)

// DEFAULT_DOMAIN_WHITELIST is a white list of domains we use frequently.
const DEFAULT_DOMAIN_WHITELIST = "google.com chromium.org skia.org"

var (
	// cookieSalt is some entropy for our encoders.
	cookieSalt = ""

	secureCookie *securecookie.SecureCookie = nil

	// oauthConfig is the OAuth 2.0 client configuration.
	oauthConfig = &oauth.Config{
		ClientId:     "not-a-valid-client-id",
		ClientSecret: "not-a-valid-client-secret",
		Scope:        DEFAULT_SCOPE,
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://accounts.google.com/o/oauth2/token",
		RedirectURL:  "http://localhost:8000/oauth2callback/",

		// We don't need a refresh token, we'll just go through the approval flow again.
		AccessType: "online",

		// And when we go through the approval flow again don't stop if they've already approved once.
		ApprovalPrompt: "auto",
	}

	// activeDomainWhiteList is the list of domains that are allowed to log in.
	activeDomainWhiteList map[string]bool

	// activeEmailWhiteList is the list of whitelisted email addresses.
	activeEmailWhiteList map[string]bool
)

type Session struct {
	Email     string
	AuthScope string
	Token     *oauth.Token
}

// Init must be called before any other methods.
//
// The Client ID, Client Secret, and Redirect URL are listed in the Google
// Developers Console. The authWhiteList is the space separated list of domains
// and email addresses that are allowed to log in.
func Init(clientId, clientSecret, redirectURL, salt, scope string, authWhiteList string, local bool) {
	cookieSalt = salt
	secureCookie = securecookie.New([]byte(salt), nil)
	oauthConfig.ClientId = clientId
	oauthConfig.ClientSecret = clientSecret
	oauthConfig.RedirectURL = redirectURL
	oauthConfig.Scope = scope

	// If we are in the cloud and there is a whitelist in meta data then use the
	// meta data version.
	if !local {
		// We allow for meta data to not be present.
		whiteList, err := metadata.Get(metadata.AUTH_WHITE_LIST)
		if err != nil {
			glog.Infof("Unable to retrieve auth whitelist from meta data. Error:", err)
		} else {
			authWhiteList = whiteList
		}
	}
	activeDomainWhiteList, activeEmailWhiteList = splitAuthWhiteList(authWhiteList)
}

// LoginURL returns a URL that the user is to be directed to for login.
func LoginURL(w http.ResponseWriter, r *http.Request) string {
	// Check for a session id, if not there then assign one, and add it to the redirect URL.
	session, err := r.Cookie(SESSION_COOKIE_NAME)
	state := ""
	if err != nil || session.Value == "" {
		state, err = util.GenerateID()
		if err != nil {
			glog.Errorf("Failed to create a session token: %s", err)
			return ""
		}
		cookie := &http.Cookie{
			Name:     SESSION_COOKIE_NAME,
			Value:    state,
			Path:     "/",
			HttpOnly: true,
			Expires:  time.Now().Add(365 * 24 * time.Hour),
		}
		http.SetCookie(w, cookie)
	} else {
		state = session.Value
	}

	redirect := r.Referer()
	if redirect == "" {
		redirect = "/"
	}
	// Append the current URL to the state, in a way that's safe from tampering,
	// so that we can use it on the rebound. So the state we pass in has the
	// form:
	//
	//   <sessionid>:<hash(salt + original url)>:<original url>
	//
	// Note that the sessionid and the hash are hex values and so won't contain
	// any colons.  To break this up when returned from the server just use
	// strings.SplitN(s, ":", 3) which will ignore any colons found in the
	// Referral URL.
	//
	// On the receiving side we need to recompute the hash and compare against
	// the hash passed in, and only if they match should the redirect URL be
	// trusted.
	state = fmt.Sprintf("%s:%x:%s", state, sha256.Sum256([]byte(cookieSalt+redirect)), redirect)

	return oauthConfig.AuthCodeURL(state)
}

func getSession(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(COOKIE_NAME)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := secureCookie.Decode(COOKIE_NAME, cookie.Value, &s); err != nil {
		return nil, err
	}
	if s.AuthScope != oauthConfig.Scope {
		return nil, fmt.Errorf("Stored auth scope differs from expected (%s vs %s)", oauthConfig.Scope, s.AuthScope)
	}
	return &s, nil
}

// LoggedInAs returns the user's ID, i.e. their email address, if they are
// logged in, and "" if they are not logged in.
func LoggedInAs(r *http.Request) string {
	s, err := getSession(r)
	if err != nil {
		return ""
	}
	return s.Email
}

// A JSON Web Token can contain much info, such as 'iss' and 'sub'. We don't care about
// that, we only want one field which is 'email'.
//
// {
//   "iss":"accounts.google.com",
//   "sub":"110642259984599645813",
//   "email":"jcgregorio@google.com",
//   ...
// }
type decodedIDToken struct {
	Email string `json:"email"`
}

// CookieFor creates an encoded Cookie for the given user id.
func CookieFor(value *Session) (*http.Cookie, error) {
	encoded, err := secureCookie.Encode(COOKIE_NAME, value)
	if err != nil {
		return nil, fmt.Errorf("Failed to encode cookie")
	}
	return &http.Cookie{
		Name:     COOKIE_NAME,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(365 * 24 * time.Hour),
	}, nil
}

func setSkIDCookieValue(w http.ResponseWriter, value *Session) {
	cookie, err := CookieFor(value)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s", err), 500)
		return
	}
	http.SetCookie(w, cookie)
}

// LogoutHandler logs the user out by overwriting the cookie with a blank email
// address.
//
// Note that this doesn't revoke the 'email' grant, so logging in later will
// still be fast. Users can always visit
//
//   https://security.google.com/settings/security/permissions
//
// to revoke any grants they make.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	glog.Infof("LogoutHandler\n")
	setSkIDCookieValue(w, &Session{})
	http.Redirect(w, r, r.FormValue("redirect"), 302)
}

// OAuth2CallbackHandler must be attached at a handler that matches
// the callback URL registered in the APIs Console. In this case
// "/oauth2callback".
func OAuth2CallbackHandler(w http.ResponseWriter, r *http.Request) {
	glog.Infof("OAuth2CallbackHandler\n")
	cookie, err := r.Cookie(SESSION_COOKIE_NAME)
	if err != nil || cookie.Value == "" {
		http.Error(w, "Invalid session state.", 500)
		return
	}

	state := r.FormValue("state")
	stateParts := strings.SplitN(state, ":", 3)
	redirect := "/"
	// If the state contains a redirect URL.
	if len(stateParts) == 3 {
		// state has this form:   <sessionid>:<hash(salt + original url)>:<original url>
		// See LoginURL for more details.
		state = stateParts[0]
		hash := stateParts[1]
		url := stateParts[2]
		expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(cookieSalt+url)))
		if hash == expectedHash {
			redirect = url
		} else {
			glog.Warning("Got an invalid redirect: %s != %s", hash, expectedHash)
		}
	}
	if state != cookie.Value {
		http.Error(w, "Session state doesn't match callback state.", 500)
		return
	}

	code := r.FormValue("code")
	glog.Infof("Code: %s ", code[:5])
	transport := &oauth.Transport{
		Config: oauthConfig,
		Transport: &http.Transport{
			Dial: util.DialTimeout,
		},
	}
	token, err := transport.Exchange(code)
	if err != nil {
		glog.Errorf("Failed to authenticate: %s", err)
		http.Error(w, "Failed to authenticate.", 500)
		return
	}
	// idToken is a JSON Web Token. We only need to decode the token, we do not
	// need to validate the token because it came to us over HTTPS directly from
	// Google's servers.
	idToken := token.Extra["id_token"]
	// The id token is actually three base64 encoded parts that are "." separated.
	segments := strings.Split(idToken, ".")
	if len(segments) != 3 {
		http.Error(w, "Invalid id_token.", 500)
		return
	}
	// Now base64 decode the middle segment, which decodes to JSON.
	padding := 4 - (len(segments[1]) % 4)
	if padding == 4 {
		padding = 0
	}
	middle := segments[1] + strings.Repeat("=", padding)
	b, err := base64.URLEncoding.DecodeString(middle)
	if err != nil {
		glog.Errorf("Failed to base64 decode middle part of token: %s From: %#v", middle, segments)
		http.Error(w, "Failed to base64 decode id_token.", 500)
		return
	}
	// Finally decode the JSON.
	decoded := &decodedIDToken{}
	if err := json.Unmarshal(b, decoded); err != nil {
		glog.Errorf("Failed to JSON decode token: %s", string(b))
		http.Error(w, "Failed to JSON decode id_token.", 500)
		return
	}

	email := strings.ToLower(decoded.Email)
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		http.Error(w, "Invalid email address received.", 500)
		return
	}

	if !activeDomainWhiteList[parts[1]] && !activeEmailWhiteList[email] {
		http.Error(w, "Accounts from your domain are not allowed or your email address is not white listed.", 500)
		return
	}
	s := Session{
		Email:     email,
		AuthScope: oauthConfig.Scope,
		Token:     token,
	}
	setSkIDCookieValue(w, &s)
	http.Redirect(w, r, redirect, 302)
}

// StatusHandler returns the login status of the user as JSON that looks like:
//
// {
//   "Email": "fred@example.com",
//   "LoginURL": "https://..."
// }
//
func StatusHandler(w http.ResponseWriter, r *http.Request) {
	glog.Infof("StatusHandler\n")
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	body := map[string]string{
		"Email":    LoggedInAs(r),
		"LoginURL": LoginURL(w, r),
	}
	if err := enc.Encode(body); err != nil {
		glog.Errorf("Failed to encode Login status to JSON: %s", err)
	}
}

// GetHttpClient returns a http.Client which performs authenticated requests as
// the logged-in user.
func GetHttpClient(r *http.Request) *http.Client {
	s, err := getSession(r)
	if err != nil {
		glog.Errorf("Failed to get session state; falling back to default http client.")
		return &http.Client{}
	}
	t := oauth.Transport{
		Config: oauthConfig,
		Token:  s.Token,
	}
	return t.Client()
}

// ForceAuth is middleware that enforces authentication
// before the wrapped handler is called. oauthCallbackPath is the
// URL path that the user is redirected to at the end of the auth flow.
func ForceAuth(h http.Handler, oauthCallbackPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userId := LoggedInAs(r)
		if userId == "" {
			// If this is not the oauth callback then redirect.
			if !strings.HasPrefix(r.URL.Path, oauthCallbackPath) {
				redirectUrl := LoginURL(w, r)
				glog.Infof("Redirect URL: %s", redirectUrl)
				if redirectUrl == "" {
					util.ReportError(w, r, fmt.Errorf("Unable to get redirect URL."), "Redirect to login failed:")
					return
				}
				http.Redirect(w, r, redirectUrl, http.StatusTemporaryRedirect)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func splitAuthWhiteList(whiteList string) (map[string]bool, map[string]bool) {
	domains := map[string]bool{}
	emails := map[string]bool{}

	for _, entry := range strings.Fields(whiteList) {
		trimmed := strings.ToLower(strings.TrimSpace(entry))
		if strings.Contains(trimmed, "@") {
			emails[trimmed] = true
		} else {
			domains[trimmed] = true
		}
	}

	return domains, emails
}
