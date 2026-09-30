package typedsandbox

// The closed refusal-site vocabulary. Each token names exactly one terminal
// exit-125 site on the supervisor's pre-report path or on the dispatcher's
// pre-Supervisor path. A token is a compile-time constant, never derived from
// an argument, a path or an error, so the frame cannot carry private content.
const (
	SiteIdentity   = "identity"
	SiteTimer      = "timer"
	SiteBootstrap  = "bootstrap"
	SiteBootNow    = "bootnow"
	SiteSignal     = "signal"
	SiteAllowance  = "allowance"
	SiteProcess    = "process"
	SiteScratch    = "scratch"
	SiteEncode     = "encode"
	SiteSelector   = "selector"
	SiteArgv       = "argv"
	refusalSiteMax = "bootstrap"
)

// refusalSitePrefix frames the token so a host-side reader can distinguish it
// from any other bytes that reach the attach stream.
const refusalSitePrefix = "phebs_site="

// refusalSiteFrameBytes bounds the frame. It is derived from the longest token
// in the vocabulary, so no token can be truncated by the copy below.
const refusalSiteFrameBytes = len(refusalSitePrefix) + len(refusalSiteMax) + 1

// refusalSiteFrame builds the exact wire frame for a token.
func refusalSiteFrame(token string) []byte {
	var frame [refusalSiteFrameBytes]byte
	n := copy(frame[:], refusalSitePrefix)
	n += copy(frame[n:len(frame)-1], token)
	frame[n] = '\n'
	return frame[:n+1]
}

// RefuseSite names the terminal refusal site on fd 2 before the process exits
// 125 without a report. Every call site is already a run-ending refusal, so
// this cannot make an accepting run emit stderr. One bounded write, never
// retried: an unavailable transport cannot prevent the exit, which is the same
// discipline watchdogExit applies to its own fd-2 frame.
func RefuseSite(token string) { writeRefusalSite(refusalSiteFrame(token)) }
