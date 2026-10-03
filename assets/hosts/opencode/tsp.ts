// tsp for OpenCode: transport disabled, adapter retained.
//
// The automatic session.idle path ran an unbound repo-wide check with no session binding
// behind it, so that path is removed (fail-closed) until session-scoped evidence is
// available. This export stays so an already-copied plugin file keeps loading; on its own
// it shows no notices. Manual checking from a shell is unaffected — see assets/hosts/README.md.
//
// Install: copy to .opencode/plugin/tsp.ts (project) or ~/.config/opencode/plugin/
// Requires: tsp on PATH for manual use.
import type { Plugin } from "@opencode-ai/plugin"

export const TPP: Plugin = async () => {
  return {
    event: async () => {
      // Intentionally inert: no automatic notices until session-scoped evidence exists.
    },
  }
}
