// Gas Town Pi Extension — Enhanced (role-aware mail delivery)
// Deploys the same lifecycle hooks as Claude's settings-autonomous.json
// but using pi's extension API.
//
// Events mapped:
//   session_start       → gt prime --hook (capture context)
//   before_agent_start  → inject captured context + worker mail checks
//   tool_call           → gt tap guard pr-workflow (on git push/pr create)
//   session_shutdown    → gt costs record
//
// Human coordination surfaces (Mayor/top layer) are mail-first and never inject
// mail into the active chat buffer. Autonomous workers may still receive injected
// mail/context for propulsion.
//
// Loaded via: pi -e gastown-hooks.js

export default (pi) => {
  const role = (process.env.GT_ROLE || "").toLowerCase();
  let primeContext = null;
  let contextInjected = false;
  let lastMailCheck = 0;

  const isHumanCoordinationSurface = () =>
    role === "mayor" || role === "mayor/" || role === "overseer" || role === "human";

  const shouldInjectMail = () => !isHumanCoordinationSurface();

  // SessionStart — run gt prime and capture context for injection
  pi.on("session_start", async (event, context) => {
    try {
      const result = await pi.exec("gt", ["prime", "--hook"]);
      if (result.code === 0 && result.stdout.trim()) {
        primeContext = result.stdout.trim();
        console.error("[gastown] gt prime captured (" + primeContext.length + " chars)");
      } else {
        console.error("[gastown] gt prime returned no output (code=" + result.code + ")");
      }
    } catch (e) {
      console.error("[gastown] gt prime failed:", e.message);
    }

  });

  // BeforeAgentStart — inject prime context + worker mail checks
  pi.on("before_agent_start", async (event, context) => {
    let mailContext = null;

    // Check mail for autonomous workers only (throttled to once per 30s).
    // Mayor/human coordination surfaces use durable mail + passive Pi UI instead.
    const now = Date.now();
    if (shouldInjectMail() && now - lastMailCheck >= 30000) {
      lastMailCheck = now;
      try {
        const mailResult = await pi.exec("gt", ["mail", "check", "--inject"]);
        if (mailResult.code === 0 && mailResult.stdout.trim()) {
          mailContext = mailResult.stdout.trim();
          console.error("[gastown] mail check: new mail found");
        }
      } catch (e) {
        console.error("[gastown] per-prompt mail check failed:", e.message);
      }
    }

    // Inject prime context on first prompt
    if (primeContext && !contextInjected) {
      contextInjected = true;
      console.error("[gastown] injecting prime context into session");
      const result = {
        message: {
          customType: "gastown-prime",
          content: primeContext,
          display: false,
        },
        systemPrompt: event.systemPrompt + "\n\n" + primeContext,
      };
      if (mailContext) {
        result.systemPrompt += "\n\n" + mailContext;
        result.message.content += "\n\n" + mailContext;
      }
      return result;
    }

    // After first prompt, inject mail if present
    if (mailContext) {
      return {
        message: {
          customType: "gastown-mail",
          content: mailContext,
          display: false,
        },
        systemPrompt: event.systemPrompt + "\n\n" + mailContext,
      };
    }
  });

  // PreToolUse equivalent — guard dangerous git operations
  pi.on("tool_call", async (event, context) => {
    if (event.toolName === "bash" && event.input?.command) {
      const cmd = event.input.command;
      if (
        cmd.includes("git push") ||
        cmd.includes("gh pr create") ||
        cmd.includes("git checkout -b")
      ) {
        try {
          const result = await pi.exec("gt", ["tap", "guard", "pr-workflow"]);
          if (result.code !== 0) {
            return { block: true, reason: result.stderr || "gt tap guard rejected this operation" };
          }
        } catch (e) {
          console.error("[gastown] gt tap guard failed:", e.message);
        }
      }
    }
  });

  // Stop equivalent — record API costs
  pi.on("session_shutdown", async (event, context) => {
    try {
      await pi.exec("gt", ["costs", "record"]);
    } catch (e) {
      console.error("[gastown] gt costs record failed:", e.message);
    }
  });
};
