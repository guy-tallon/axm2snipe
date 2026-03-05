// Package notify sends Slack webhook notifications for axm2snipe events.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zchee/abm"

	"github.com/CampusTech/axm2snipe/abmclient"
)

var log = logrus.New()

// SetLogLevel sets the logger level for the notify package.
func SetLogLevel(level logrus.Level) {
	log.SetLevel(level)
}

// Notifier sends Slack webhook notifications.
type Notifier struct {
	webhookURL string
	snipeURL   string
	httpClient *http.Client
}

// NewNotifier creates a new Slack notifier. Returns nil if webhookURL is empty.
func NewNotifier(webhookURL, snipeURL string) *Notifier {
	if webhookURL == "" {
		return nil
	}
	return &Notifier{
		webhookURL: webhookURL,
		snipeURL:   strings.TrimRight(snipeURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// NotifyNewAsset sends a Slack notification about a newly created asset.
func (n *Notifier) NotifyNewAsset(ctx context.Context, device abmclient.Device, modelName string, appleCare *abmclient.AppleCareCoverage) {
	if n == nil {
		return
	}

	attrs := device.Attributes
	if attrs == nil {
		return
	}

	serial := attrs.SerialNumber
	blocks := n.buildNewAssetBlocks(attrs, device.AssignedServer, modelName, appleCare)

	payload := map[string]any{
		"blocks": blocks,
		"text":   fmt.Sprintf("New device added to Snipe-IT: %s (%s)", modelName, serial),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.WithError(err).Error("Failed to marshal Slack payload")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		log.WithError(err).Error("Failed to create Slack request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		log.WithError(err).Error("Failed to send Slack notification")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.WithField("status", resp.StatusCode).Error("Slack webhook returned non-200 status")
		return
	}

	log.WithField("serial", serial).Debug("Sent Slack notification for new asset")
}

func (n *Notifier) buildNewAssetBlocks(attrs *abm.OrgDeviceAttributes, assignedServer, modelName string, appleCare *abmclient.AppleCareCoverage) []map[string]any {
	serial := attrs.SerialNumber

	// Header
	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]any{
				"type": "plain_text",
				"text": ":new: New Device Added to Snipe-IT",
			},
		},
	}

	// Device info section
	deviceFields := []map[string]any{
		mrkdwnField("*Model*\n" + modelName),
		mrkdwnField("*Serial Number*\n`" + serial + "`"),
	}

	if attrs.Color != "" {
		deviceFields = append(deviceFields, mrkdwnField("*Color*\n"+titleCase(attrs.Color)))
	}
	if attrs.DeviceCapacity != "" {
		deviceFields = append(deviceFields, mrkdwnField("*Storage*\n"+attrs.DeviceCapacity+" GB"))
	}

	blocks = append(blocks, map[string]any{
		"type":   "section",
		"fields": deviceFields,
	})

	// MDM & Organization section
	var orgFields []map[string]any
	if assignedServer != "" {
		orgFields = append(orgFields, mrkdwnField("*MDM Server*\n"+assignedServer))
	}
	if !attrs.AddedToOrgDateTime.IsZero() {
		orgFields = append(orgFields, mrkdwnField("*Added to Org*\n"+attrs.AddedToOrgDateTime.Format("Jan 2, 2006")))
	}
	if string(attrs.PurchaseSourceType) != "" {
		orgFields = append(orgFields, mrkdwnField("*Purchase Source*\n"+titleCase(string(attrs.PurchaseSourceType))))
	}
	if attrs.OrderNumber != "" {
		orgFields = append(orgFields, mrkdwnField("*Order Number*\n"+attrs.OrderNumber))
	}

	if len(orgFields) > 0 {
		blocks = append(blocks, map[string]any{
			"type": "divider",
		})
		blocks = append(blocks, map[string]any{
			"type":   "section",
			"fields": orgFields,
		})
	}

	// AppleCare section
	if appleCare != nil && appleCare.Status != "" {
		var acFields []map[string]any
		statusEmoji := ":grey_question:"
		switch strings.ToLower(appleCare.Status) {
		case "active":
			statusEmoji = ":large_green_circle:"
		case "expired":
			statusEmoji = ":red_circle:"
		case "inactive":
			statusEmoji = ":white_circle:"
		}
		acFields = append(acFields, mrkdwnField("*AppleCare Status*\n"+statusEmoji+" "+titleCase(appleCare.Status)))

		if appleCare.Description != "" {
			acFields = append(acFields, mrkdwnField("*Coverage*\n"+appleCare.Description))
		}
		if !appleCare.StartDateTime.IsZero() {
			acFields = append(acFields, mrkdwnField("*Start Date*\n"+appleCare.StartDateTime.Format("Jan 2, 2006")))
		}
		if !appleCare.EndDateTime.IsZero() {
			acFields = append(acFields, mrkdwnField("*End Date*\n"+appleCare.EndDateTime.Format("Jan 2, 2006")))
		}

		blocks = append(blocks, map[string]any{
			"type": "divider",
		})
		blocks = append(blocks, map[string]any{
			"type":   "section",
			"text":   mrkdwnText(":shield: *AppleCare Coverage*"),
			"fields": acFields,
		})
	}

	// Link to Snipe-IT asset
	if n.snipeURL != "" {
		blocks = append(blocks, map[string]any{
			"type": "divider",
		})
		blocks = append(blocks, map[string]any{
			"type": "context",
			"elements": []map[string]any{
				mrkdwnText(fmt.Sprintf(":link: <"+n.snipeURL+"/hardware/bytag/%s|View in Snipe-IT> · synced by axm2snipe", serial)),
			},
		})
	}

	return blocks
}

func mrkdwnField(text string) map[string]any {
	return map[string]any{
		"type": "mrkdwn",
		"text": text,
	}
}

func mrkdwnText(text string) map[string]any {
	return map[string]any{
		"type": "mrkdwn",
		"text": text,
	}
}

func titleCase(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}
