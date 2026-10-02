package typedsandbox

// refusalSite is an index into the closed token table below. The type is
// unexported, so no caller outside this package can convert a string, an error or
// a variable into one: the only values that reach RefuseSite are the named
// constants, or an untyped integer constant, which either names one of the same
// fixed tokens or is rejected by token(). Closure is therefore a compiler property
// rather than a convention a scanner has to enforce, and a frame cannot carry an
// argument, a path, an error string or an injected newline.
type refusalSite int

// The closed refusal-site vocabulary. Each site names exactly one terminal
// exit-125 refusal reachable on the production linux/arm64 target, on the
// supervisor's pre-report path, dispatcher's pre-Supervisor path, or the
// neutral preparation worker's terminal path.
const (
	SiteIdentity refusalSite = iota
	SiteTimer
	SiteBootstrap
	SiteBootNow
	SiteSignal
	SiteAllowanceLive
	SiteAllowanceBootRead
	SiteAllowanceBootMismatch
	SiteAllowanceTimeStat
	SiteAllowanceTimeMismatch
	SiteAllowanceNowRead
	SiteAllowanceWindow
	SiteAllowanceControls
	SiteAllowanceSeal
	SiteAllowanceDigest
	SiteAllowanceBinding
	SiteProcess
	SiteScratch
	SiteEncode
	SiteSelector
	SiteArgv
	SiteWorkerInvocation
	SiteWorkerBinding
	SiteWorkerClock
	SiteWorkerControls
	SiteWorkerBuildInfo
	SiteWorkerTools
	SiteWorkerMaterialize
	SiteWorkerCompiler
	SiteWorkerPlan
	SiteWorkerVerify
	SiteWorkerFinal
	SiteWorkerResult
	SiteWorkerEncode
	SiteWorkerOutput

	refusalSiteCount
)

// refusalSiteTokens is the wire vocabulary, keyed by site so that a reordered
// constant block cannot silently relabel a site.
var refusalSiteTokens = [refusalSiteCount]string{
	SiteIdentity:              "identity",
	SiteTimer:                 "timer",
	SiteBootstrap:             "bootstrap",
	SiteBootNow:               "bootnow",
	SiteSignal:                "signal",
	SiteAllowanceLive:         "allow_live",
	SiteAllowanceBootRead:     "allow_bootread",
	SiteAllowanceBootMismatch: "allow_bootdiff",
	SiteAllowanceTimeStat:     "allow_nsstat",
	SiteAllowanceTimeMismatch: "allow_nsdiff",
	SiteAllowanceNowRead:      "allow_nowread",
	SiteAllowanceWindow:       "allow_window",
	SiteAllowanceControls:     "allow_controls",
	SiteAllowanceSeal:         "allow_seal",
	SiteAllowanceDigest:       "allow_digest",
	SiteAllowanceBinding:      "allow_binding",
	SiteProcess:               "process",
	SiteScratch:               "scratch",
	SiteEncode:                "encode",
	SiteSelector:              "selector",
	SiteArgv:                  "argv",
	SiteWorkerInvocation:      "w_invocation",
	SiteWorkerBinding:         "w_binding",
	SiteWorkerClock:           "w_clock",
	SiteWorkerControls:        "w_controls",
	SiteWorkerBuildInfo:       "w_buildinfo",
	SiteWorkerTools:           "w_tools",
	SiteWorkerMaterialize:     "w_materialize",
	SiteWorkerCompiler:        "w_compiler",
	SiteWorkerPlan:            "w_plan",
	SiteWorkerVerify:          "w_verify",
	SiteWorkerFinal:           "w_final",
	SiteWorkerResult:          "w_result",
	SiteWorkerEncode:          "w_encode",
	SiteWorkerOutput:          "w_output",
}

// refusalSiteLongestToken must be the longest entry in refusalSiteTokens. The
// frame bound is derived from it and the oracle re-measures the table's maximum
// against it, so lengthening a token without lengthening this fails a gate.
const refusalSiteLongestToken = "allow_bootread"

// refusalSitePrefix frames the token so a host-side reader can distinguish it
// from any other bytes that reach the attach stream.
const refusalSitePrefix = "phebs_site="

// refusalSiteFrameBytes bounds the frame. It is derived from the longest token
// in the vocabulary, so no token can be truncated by the copy below.
const refusalSiteFrameBytes = len(refusalSitePrefix) + len(refusalSiteLongestToken) + 1

// token returns the wire token for a site, or the empty string for an index
// outside the table. Callers treat empty as "write nothing".
func (s refusalSite) token() string {
	if s < 0 || int(s) >= len(refusalSiteTokens) {
		return ""
	}
	return refusalSiteTokens[s]
}

// refusalSiteFrame builds the exact wire frame for a site, or nil for an index
// outside the table. A host reader must require the trailing newline before
// accepting a token: a frame truncated by a saturated transport can be a prefix
// of more than one token.
func refusalSiteFrame(site refusalSite) []byte {
	token := site.token()
	if token == "" {
		return nil
	}
	var frame [refusalSiteFrameBytes]byte
	n := copy(frame[:], refusalSitePrefix)
	n += copy(frame[n:len(frame)-1], token)
	frame[n] = '\n'
	return frame[:n+1]
}

// RefuseSite names the terminal refusal site on fd 2 before the process exits
// 125. A worker frame can be carried inside its supervisor report. Every call
// site is already a run-ending refusal on the goroutine that refuses, so this
// cannot turn that goroutine's accepting path
// into a stderr producer. One bounded write, never retried: an unavailable
// transport cannot prevent the exit, which is the same discipline watchdogExit
// applies to its own fd-2 frame.
func RefuseSite(site refusalSite) { writeRefusalSite(refusalSiteFrame(site)) }
