package parser

import (
	"bytes"
	"net/url"

	"github.com/PuerkitoBio/goquery"
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
