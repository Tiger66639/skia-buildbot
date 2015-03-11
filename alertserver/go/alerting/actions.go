package alerting

import (
	"fmt"
	"strings"
	"time"

	"github.com/skia-dev/glog"
	"go.skia.org/infra/go/email"
)

const (
	EMAIL_FOOTER = `<br/><br/>
This email was generated by the Skia alert server.
To snooze or dismiss this alert, visit https://alerts.skia.org
`
	EMAIL_SUBJECT_TMPL = "Skia Alert: %s triggered at %s"
)

var (
	emailAuth *email.GMail = nil
)

// Actions are performed whenever an Alert is updated.
type Action interface {
	Fire(*Alert)
	Followup(*Alert, string)
	String() string
}

type EmailAction struct {
	to      []string
	subject string
	str     string
}

func (a *EmailAction) Fire(alert *Alert) {
	// Cache the email subject so we can send followup emails on the same thread.
	a.subject = fmt.Sprintf(EMAIL_SUBJECT_TMPL, alert.Name, time.Unix(alert.Triggered, 0).String())
	body := alert.Message + EMAIL_FOOTER
	if emailAuth == nil {
		glog.Errorf("Email auth is nil! Cannot send email!")
		return
	}
	if err := emailAuth.Send(a.to, a.subject, body); err != nil {
		glog.Errorf("Failed to send email: %s", err)
	}
}

func (a *EmailAction) Followup(alert *Alert, msg string) {
	if emailAuth == nil {
		glog.Error("Email auth is nil! Cannot send email!")
		return
	}
	if err := emailAuth.Send(a.to, a.subject, msg); err != nil {
		glog.Errorf("Failed to send email: %s", err)
	}
}

func (a *EmailAction) String() string {
	return a.str
}

func NewEmailAction(to []string, str string) Action {
	return &EmailAction{
		to:      to,
		subject: "",
		str:     str,
	}
}

type PrintAction struct{}

func (a *PrintAction) Fire(alert *Alert) {
	glog.Infof("ALERT FIRED (%s): %s", alert.Name, alert.Message)
}

func (a *PrintAction) Followup(alert *Alert, msg string) {
	glog.Infof("ALERT FOLLOWUP (%s): %s", alert.Name, msg)
}

func (a *PrintAction) String() string {
	return "Print"
}

func NewPrintAction() Action {
	return &PrintAction{}
}

func parseEmailList(str string) []string {
	split := strings.Split(str, ",")
	emails := []string{}
	for _, email := range split {
		emails = append(emails, strings.Trim(email, " "))
	}
	return emails
}

// ParseAction converts a string to an Action.
func ParseAction(str string) (Action, error) {
	if strings.HasPrefix(str, "Email(") && strings.HasSuffix(str, ")") {
		to := parseEmailList(str[6 : len(str)-1])
		return NewEmailAction(to, str), nil
	} else if str == "Print" {
		return NewPrintAction(), nil
	} else {
		return nil, fmt.Errorf("Unknown action: %q", str)
	}

}

// ParseActions parses configuration information to produce Actions for an Alert.
func ParseActions(actions []string) ([]Action, error) {
	actionsList := []Action{}
	for _, actionString := range actions {
		action, err := ParseAction(actionString)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse action: %v", err)
		}
		actionsList = append(actionsList, action)
	}
	return actionsList, nil
}
