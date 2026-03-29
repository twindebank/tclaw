package channel

// ChannelSecretKey returns the secret store key for a channel's secret (e.g. bot token).
func ChannelSecretKey(channelName string) string {
	return "channel/" + channelName + "/token"
}
