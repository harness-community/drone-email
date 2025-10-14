package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"os"

	"github.com/aymerick/douceur/inliner"
	"github.com/drone/drone-template-lib/template"
	"github.com/jaytaylor/html2text"
	log "github.com/sirupsen/logrus"
	mail "github.com/wneessen/go-mail"
)

type (
	Repo struct {
		FullName string
		Owner    string
		Name     string
		SCM      string
		Link     string
		Avatar   string
		Branch   string
		Private  bool
		Trusted  bool
	}

	Remote struct {
		URL string
	}

	Author struct {
		Name   string
		Email  string
		Avatar string
	}

	Commit struct {
		Sha     string
		Ref     string
		Branch  string
		Link    string
		Message string
		Author  Author
	}

	Build struct {
		Number   int
		Event    string
		Status   string
		Link     string
		Created  float64
		Started  float64
		Finished float64
	}

	PrevBuild struct {
		Status string
		Number int
	}

	PrevCommit struct {
		Sha string
	}

	Prev struct {
		Build  PrevBuild
		Commit PrevCommit
	}

	Job struct {
		Status   string
		ExitCode int
		Started  int64
		Finished int64
	}

	Yaml struct {
		Signed   bool
		Verified bool
	}

	Config struct {
		FromAddress    string
		FromName       string
		Host           string
		Port           int
		Username       string
		Password       string
		SkipVerify     bool
		NoStartTLS     bool
		Recipients     []string
		RecipientsFile string
		RecipientsOnly bool
		Subject        string
		Body           string
		Attachment     string
		Attachments    []string
		ClientHostname string
	}

	Plugin struct {
		Repo        Repo
		Remote      Remote
		Commit      Commit
		Build       Build
		Prev        Prev
		Job         Job
		Yaml        Yaml
		Tag         string
		PullRequest int
		DeployTo    string
		Config      Config
	}
)

// Exec will send emails over SMTP
func (p Plugin) Exec() error {
	// Build recipient list
	recipientsMap := make(map[string]struct{})

	// Add recipients from the config
	for _, recipient := range p.Config.Recipients {
		if recipient == "" {
			log.Warnf("Skipping empty recipient from config")
			continue
		}
		recipientsMap[recipient] = struct{}{}
	}

	// Add commit author's email if not already present and RecipientsOnly is false
	if !p.Config.RecipientsOnly {
		if p.Commit.Author.Email != "" {
			recipientsMap[p.Commit.Author.Email] = struct{}{}
		} else {
			log.Warn("Commit author email is empty")
		}
	}

	// Add recipients from the recipients file
	if p.Config.RecipientsFile != "" {
		f, err := os.Open(p.Config.RecipientsFile)
		if err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				recipient := scanner.Text()
				if recipient == "" {
					log.Warnf("Skipping empty recipient from file %s", p.Config.RecipientsFile)
					continue
				}
				recipientsMap[recipient] = struct{}{}
			}
		} else {
			log.Errorf("Could not open RecipientsFile %s: %v", p.Config.RecipientsFile, err)
		}
	}

	log.Infof("Recipients: %v", recipientsMap)

	// Create mail client with options
	options := []mail.Option{
		mail.WithPort(p.Config.Port),
	}

	// Set HELO hostname if provided
	if p.Config.ClientHostname != "" {
		options = append(options, mail.WithHELO(p.Config.ClientHostname))
	}

	// Add authentication if provided
	if p.Config.Username != "" && p.Config.Password != "" {
		options = append(options,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(p.Config.Username),
			mail.WithPassword(p.Config.Password),
		)
	}

	// Handle TLS configuration
	if p.Config.SkipVerify {
		options = append(options, mail.WithTLSConfig(&tls.Config{
			InsecureSkipVerify: true,
		}))
	}

	// Handle STARTTLS policy
	if p.Config.NoStartTLS {
		options = append(options, mail.WithTLSPortPolicy(mail.NoTLS))
	} else {
		options = append(options, mail.WithTLSPortPolicy(mail.TLSOpportunistic))
	}

	client, err := mail.NewClient(p.Config.Host, options...)
	if err != nil {
		log.Errorf("Error creating mail client: %v", err)
		return err
	}

	// Prepare template context
	type Context struct {
		Repo        Repo
		Remote      Remote
		Commit      Commit
		Build       Build
		Prev        Prev
		Job         Job
		Yaml        Yaml
		Tag         string
		PullRequest int
		DeployTo    string
	}
	ctx := Context{
		Repo:        p.Repo,
		Remote:      p.Remote,
		Commit:      p.Commit,
		Build:       p.Build,
		Prev:        p.Prev,
		Job:         p.Job,
		Yaml:        p.Yaml,
		Tag:         p.Tag,
		PullRequest: p.PullRequest,
		DeployTo:    p.DeployTo,
	}

	// Render body in HTML and plain text
	renderedBody, err := template.RenderTrim(p.Config.Body, ctx)
	if err != nil {
		log.Errorf("Could not render body template: %v", err)
		return err
	}

	html, err := inliner.Inline(renderedBody)
	if err != nil {
		log.Errorf("Could not inline rendered body: %v", err)
		return err
	}

	plainBody, err := html2text.FromString(html)
	if err != nil {
		log.Errorf("Could not convert html to text: %v", err)
		return err
	}

	// Render subject
	subject, err := template.RenderTrim(p.Config.Subject, ctx)
	if err != nil {
		log.Errorf("Could not render subject template: %v", err)
		return err
	}

	// Dial connection once and reuse for all recipients
	if err := client.DialWithContext(context.Background()); err != nil {
		log.Errorf("Error while dialing SMTP server: %v", err)
		return err
	}
	defer client.Close()

	// Send emails to each recipient
	for recipient := range recipientsMap {
		msg := mail.NewMsg()

		// Set From header with optional name
		if p.Config.FromName != "" {
			if err := msg.FromFormat(p.Config.FromName, p.Config.FromAddress); err != nil {
				log.Errorf("Could not set From header: %v", err)
				return err
			}
		} else {
			if err := msg.From(p.Config.FromAddress); err != nil {
				log.Errorf("Could not set From header: %v", err)
				return err
			}
		}

		// Set To header
		if err := msg.To(recipient); err != nil {
			log.Errorf("Could not set To header: %v", err)
			return err
		}

		// Set Subject
		msg.Subject(subject)

		// Set body with plain text and HTML alternatives
		msg.SetBodyString(mail.TypeTextPlain, plainBody)
		msg.AddAlternativeString(mail.TypeTextHTML, html)

		// Add single attachment if specified
		if p.Config.Attachment != "" {
			if _, err := os.Stat(p.Config.Attachment); err == nil {
				msg.AttachFile(p.Config.Attachment)
			}
		}

		// Add multiple attachments
		for _, attachment := range p.Config.Attachments {
			if _, err := os.Stat(attachment); err == nil {
				msg.AttachFile(attachment)
			}
		}

		// Send using existing connection
		if err := client.Send(msg); err != nil {
			log.Errorf("Could not send email to %q: %v", recipient, err)
			return err
		}
	}

	return nil
}
