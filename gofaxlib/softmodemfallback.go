package gofaxlib

import (
	"fmt"
	"github.com/fiorix/go-eventsocket/eventsocket"
	"time"
)

const (
	modDbFallbackRealm = "fallback"
)

// GetSoftmodemFallback checks if fallback to SpanDSP's softmodem (no T.38)
// should be enabled for the given callerid number
func GetSoftmodemFallback(c *eventsocket.Connection, cidnum string) (bool, error) {
	if !Config.FreeSwitch.SoftmodemFallback || cidnum == "" {
		return false, nil
	}

	var err error
	if c == nil {
		c, err = eventsocket.Dial(Config.FreeSwitch.EventClientSocket, Config.FreeSwitch.EventClientSocketPassword)
		if err != nil {
			return false, err
		}
		defer c.Close()
	}

	exists, err := FreeSwitchDBExists(c, modDbFallbackRealm, cidnum)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// SetSoftmodemFallback saves the given softmodem fallback setting for a caller id
// to FreeSWITCH's mod_db
func SetSoftmodemFallback(c *eventsocket.Connection, cidnum string, enabled bool) error {
	if !Config.FreeSwitch.SoftmodemFallback || cidnum == "" {
		return nil
	}

	var err error
	if c == nil {
		c, err = eventsocket.Dial(Config.FreeSwitch.EventClientSocket, Config.FreeSwitch.EventClientSocketPassword)
		if err != nil {
			return err
		}
		defer c.Close()
	}

	return FreeSwitchDBInsert(c, modDbFallbackRealm, cidnum, fmt.Sprintf("%d", time.Now().Unix()))
}
