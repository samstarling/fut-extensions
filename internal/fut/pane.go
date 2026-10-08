package fut

// PublishPane sets one pane-scoped token.
func (c *Client) PublishPane(extensionID, paneID, token, value string) error {
	_, err := c.run("token", "publish", extensionID, token, value, "--pane-id", paneID)
	return err
}
