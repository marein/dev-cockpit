package opencode

import (
	"os"
	"path/filepath"
)

// notifyEnv carries the notify inbox directory into every session the
// cockpit starts, through the tmux session environment. The injected plugin
// below reads it and stays inert without it, so the assistant's own opencode
// runs, the pre-create server and sessions somebody starts by hand write
// nothing.
const notifyEnv = "DEV_COCKPIT_NOTIFY_INBOX"

// notifyPluginFile is the plugin's file name inside the config directory's
// plugin folder, which every opencode process scans at start (verified on
// 1.18.23, the loader globs {plugin,plugins}/*.{ts,js}).
const notifyPluginFile = "dev-cockpit-notify.js"

// notifyPlugin is the injected opencode plugin, the opencode counterpart of
// claude's Stop/Notification hooks. opencode has no per session hook flag,
// but its plugin system hands every server event to an event hook, so the
// signals are read off the event bus (verified on 1.18.23): a finished turn
// is `session.idle`, an asked permission is `permission.asked` (the v2 name
// travels along for opencode's ongoing event rename), and both carry the
// session id in their properties. Each signal becomes one JSON file in the
// inbox named by the environment variable, in the claude hook shape the
// inbox poller consumes, written to a .tmp name first and renamed to .json
// so the poller only ever reads complete files.
//
// Three readings make the raw events usable. The busy guard: `session.idle`
// says a session is idle, not that work ended, so it only counts after a
// `session.status` reported the session busy, or a TUI opening an idle
// session would ring for nothing. The session lookup: a subagent's child
// session idles too and is nobody's news (skipped via parentID), and a
// conversation handed over to a terminal is listed under the cockpit's own
// id, which its metadata carries (the same mapping session.go reads out of
// the database). The reply grace: a session the cockpit starts with --auto
// answers its own permissions, the TUI replies once a few milliseconds
// after the ask (external_directory and doom_loop are the everyday cases),
// and the server publishes the ask anyway, so an ask nobody was going to
// see would ring in the middle of a turn. The plugin therefore holds every
// ask for two seconds before it becomes news: the ask carries the request
// id as `id`, the reply carries it as `requestID` (verified on 1.18.23,
// the v2 events share both shapes), so a reply arriving inside the grace
// clears the pending timer and nothing is written, and an ask without one
// drops exactly one file, after the wait.
const notifyPlugin = `export const DevCockpitNotify = async ({ client }) => {
  const inbox = process.env["` + notifyEnv + `"]
  if (!inbox) return {}
  const fs = await import("node:fs")
  const path = await import("node:path")
  let counter = 0
  const busy = new Set()
  const pending = new Map()
  const drop = (sessionID, name, message) => {
    const payload = JSON.stringify({ session_id: sessionID, hook_event_name: name, message: message })
    const file = path.join(inbox, Date.now() + "-" + process.pid + "-" + counter++)
    try {
      fs.mkdirSync(inbox, { recursive: true })
      fs.writeFileSync(file + ".tmp", payload)
      fs.renameSync(file + ".tmp", file + ".json")
    } catch {}
  }
  const target = async (sessionID) => {
    try {
      const res = await client.session.get({ path: { id: sessionID } })
      const info = res && res.data ? res.data : res
      if (!info || info.parentID) return ""
      const mapped = info.metadata && info.metadata.devCockpitSessionID
      return typeof mapped === "string" && mapped !== "" ? mapped : sessionID
    } catch {
      return sessionID
    }
  }
  return {
    event: async ({ event }) => {
      const type = event && event.type
      const properties = (event && event.properties) || {}
      const sessionID = properties.sessionID
      if (!sessionID) return
      if (type === "session.status") {
        if (properties.status && properties.status.type === "busy") busy.add(sessionID)
        return
      }
      if (type === "session.idle") {
        if (!busy.delete(sessionID)) return
        const id = await target(sessionID)
        if (id) drop(id, "Stop", "")
        return
      }
      if (type === "permission.asked" || type === "permission.v2.asked") {
        const requestID = properties.id
        const timer = setTimeout(async () => {
          pending.delete(requestID)
          const id = await target(sessionID)
          if (id) drop(id, "Notification", "Permission requested")
        }, 2000)
        if (timer.unref) timer.unref()
        if (requestID) pending.set(requestID, timer)
        return
      }
      if (type === "permission.replied" || type === "permission.v2.replied") {
        const timer = pending.get(properties.requestID)
        if (timer !== undefined) {
          clearTimeout(timer)
          pending.delete(properties.requestID)
        }
      }
    },
  }
}
`

// ensureNotifyPlugin writes the notification plugin into the config
// directory's plugin folder. The file is deliberately instance free, every
// path it needs arrives per session through the environment, so every
// cockpit on the machine writes the same bytes and nothing removes it at
// stop: without the environment variable it does nothing at all.
func ensureNotifyPlugin() error {
	return ensureGeneratedFile(filepath.Join(configDir(), "plugin", notifyPluginFile), notifyPlugin)
}

// ensureGeneratedFile writes one of the cockpit's generated files. The paths
// carry the cockpit's own name, so whatever holds one is the cockpit's to
// rewrite, and rewriting unconditionally is what keeps the files current
// across releases; only an unchanged file writes nothing.
func ensureGeneratedFile(path, content string) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
