package robots

import (
	"github.com/temoto/robotstxt"
)

func parseRobotsTxt(content []byte) (*robotstxt.RobotsData, error) {
	robots, err := robotstxt.FromBytes(content)
	if err != nil {
		return nil, err
	}

	return robots, nil
}
