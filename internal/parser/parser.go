package parser

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	collapseWS = regexp.MustCompile(`\s+`)

	excludeTags = []string{"script", "style", "noscript", "nav", "footer", "header", "aside", "form", "input", "button", "select", "textarea", "iframe", "canvas", "svg"}
)

func ExtractURLs(html []byte, baseURL string) ([]string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}

	var urls []string
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			ref, err := url.Parse(href)
			if err != nil {
				return
			}

			absolute := base.ResolveReference(ref)
			urls = append(urls, absolute.String())
		}
	})

	return urls, nil
}

func SanitizeURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}

	parsed.Fragment = ""
	return parsed.String(), true
}

func ExtractText(html []byte) (string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return "", err
	}

	for _, tag := range excludeTags {
		doc.Find(tag).Remove()
	}

	content := doc.Text()
	content = collapseWS.ReplaceAllString(content, " ")
	content = strings.TrimSpace(content)

	return content, nil
}
