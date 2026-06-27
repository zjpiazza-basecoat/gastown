// Gas Town Pi Extension — Enhanced (role-aware mail delivery)
// Deploys the same lifecycle hooks as Claude's settings-autonomous.json
// but using pi's extension API.
//
// Events mapped:
//   session_start       → gt prime --hook (capture context)
//   before_agent_start  → inject captured context + worker mail checks
//   tool_call           → gt tap guard pr-workflow (on PR workflow commands)
//   session_shutdown    → gt costs record
//
// Human coordination surfaces (Mayor/top layer) are mail-first and never inject
// mail into the active chat buffer. Autonomous workers may still receive injected
// mail/context for propulsion.
//
// Loaded via: pi -e gastown-hooks.js

import { Key, matchesKey, truncateToWidth, visibleWidth, wrapTextWithAnsi } from "@earendil-works/pi-tui";

const gtBin = process.env.GT_BIN || "gt";

const healthSeverity = (report) => {
  if (!report) return "unknown";
  if (!report.server?.running) return "unhealthy";
  if ((report.processes?.zombie_count || 0) > 0) return "unhealthy";
  if ((report.pollution?.length || 0) > 0) return "degraded";
  if ((report.orphans?.length || 0) > 0) return "degraded";
  if (report.backups?.dolt_stale || report.backups?.jsonl_stale) return "degraded";
  if ((report.server?.latency_ms || 0) > 5000) return "degraded";
  return "healthy";
};

const healthSummary = (report) => {
  const severity = healthSeverity(report);
  if (!report) return { severity, label: "gt health unknown", icon: "○" };
  if (severity === "unhealthy") return { severity, label: "gt unhealthy", icon: "✕" };
  if (severity === "degraded") return { severity, label: "gt degraded", icon: "⚠" };
  return { severity, label: "gt healthy", icon: "✓" };
};

const formatAge = (seconds) => {
  if (!seconds && seconds !== 0) return "unknown";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
};

const loadHealth = async (pi, cwd) => {
  try {
    const result = await pi.exec(gtBin, ["health", "--json"], { cwd, timeout: 15000 });
    if (result.code !== 0) {
      return {
        report: null,
        error: (result.stderr || result.stdout || `gt health exited ${result.code}`).trim(),
        capturedAt: new Date(),
      };
    }
    return { report: JSON.parse(result.stdout), error: null, capturedAt: new Date() };
  } catch (error) {
    return { report: null, error: error?.message || String(error), capturedAt: new Date() };
  }
};

const updateHealthStatus = (ctx, state) => {
  if (!ctx.hasUI) return;
  const theme = ctx.ui.theme;
  const summary = healthSummary(state.report);
  const color = summary.severity === "healthy"
    ? "success"
    : summary.severity === "degraded"
      ? "warning"
      : summary.severity === "unhealthy"
        ? "error"
        : "dim";
  ctx.ui.setStatus(
    "gastown-health",
    theme.fg(color, `${summary.icon} ${summary.label}`) +
      (state.capturedAt ? theme.fg("dim", ` ${state.capturedAt.toLocaleTimeString()}`) : "")
  );
};

const healthDetailLines = (state) => {
  if (state.error) {
    return ["gt health failed", "", state.error];
  }
  const report = state.report;
  if (!report) return ["No health report available."];
  const server = report.server || {};
  const backups = report.backups || {};
  const processes = report.processes || {};
  const lines = [];
  lines.push(`Dolt: ${server.running ? "running" : "not running"}${server.pid ? ` (pid ${server.pid})` : ""}`);
  if (server.port) lines.push(`Port: ${server.port}`);
  if (server.latency_ms || server.latency_ms === 0) lines.push(`Latency: ${server.latency_ms}ms`);
  if (server.connections || server.connections === 0) lines.push(`Connections: ${server.connections}/${server.max_connections || "?"}`);
  if (server.disk_usage_human) lines.push(`Disk: ${server.disk_usage_human}`);
  lines.push("");
  lines.push("Databases:");
  for (const db of report.databases || []) {
    lines.push(`  ${db.name}: ${db.open_issues}/${db.issues} open issues, ${db.open_wisps}/${db.wisps} open wisps, ${db.commits} commits`);
  }
  if (!(report.databases || []).length) lines.push("  none reported");
  lines.push("");
  lines.push(`Pollution: ${(report.pollution || []).length ? `${report.pollution.length} suspicious records` : "none"}`);
  for (const p of (report.pollution || []).slice(0, 8)) {
    lines.push(`  ${p.database}/${p.id}: ${p.pattern}`);
  }
  lines.push(`Orphan DBs: ${(report.orphans || []).length ? report.orphans.map((o) => `${o.name}${o.size ? ` (${o.size})` : ""}`).join(", ") : "none"}`);
  lines.push(`Backups: dolt ${backups.dolt_freshness || formatAge(backups.dolt_age_seconds)}${backups.dolt_stale ? " STALE" : ""}, jsonl ${backups.jsonl_freshness || formatAge(backups.jsonl_age_seconds)}${backups.jsonl_stale ? " STALE" : ""}`);
  lines.push(`Zombie Dolt processes: ${processes.zombie_count || 0}${(processes.zombie_pids || []).length ? ` (${processes.zombie_pids.join(", ")})` : ""}`);
  return lines;
};

const createHealthModal = (initialState, ctx, done) => {
  let state = initialState;
  let loading = false;
  let handle = null;

  const refresh = async () => {
    loading = true;
    handle?.requestRender();
    state = await loadHealth(piRef, ctx.cwd);
    updateHealthStatus(ctx, state);
    loading = false;
    handle?.requestRender();
  };

  // Set by showHealthModal immediately after construction. Kept indirect so the
  // component can refresh from key input without rebuilding the overlay.
  let piRef = null;
  const component = {
    setPi(pi) { piRef = pi; },
    setHandle(h) { handle = h; },
    handleInput(data) {
      if (matchesKey(data, Key.escape) || data === "q") {
        done(null);
      } else if (data === "r" || data === "R") {
        refresh();
      }
    },
    invalidate() {},
    render(width) {
      const theme = ctx.ui.theme;
      const innerWidth = Math.max(20, width - 4);
      const summary = healthSummary(state.report);
      const color = summary.severity === "healthy" ? "success" : summary.severity === "degraded" ? "warning" : summary.severity === "unhealthy" ? "error" : "dim";
      const title = theme.fg(color, theme.bold(`${summary.icon} Gas Town Health`));
      const when = state.capturedAt ? theme.fg("dim", `captured ${state.capturedAt.toLocaleTimeString()}`) : theme.fg("dim", "not captured");
      const body = healthDetailLines(state).flatMap((line) => wrapTextWithAnsi(line, innerWidth));
      const border = (left, fill, right) => truncateToWidth(`${left}${fill.repeat(Math.max(0, width - 2))}${right}`, width);
      const row = (content) => {
        const clipped = truncateToWidth(content, innerWidth);
        return truncateToWidth(`│ ${clipped}${" ".repeat(Math.max(0, innerWidth - visibleWidth(clipped)))} │`, width);
      };
      const lines = [
        border("╭", "─", "╮"),
        row(title),
        row(when),
        border("├", "─", "┤"),
      ];
      for (const line of body.slice(0, 24)) {
        lines.push(row(line));
      }
      if (loading) lines.push(row(theme.fg("accent", "refreshing…")));
      lines.push(border("├", "─", "┤"));
      lines.push(row(theme.fg("dim", "r refresh • esc/q close")));
      lines.push(border("╰", "─", "╯"));
      return lines;
    },
  };
  return component;
};

const createProposalModal = (proposal, ctx, done) => ({
  handleInput(data) {
    if (matchesKey(data, Key.enter) || data === "y" || data === "Y") done("approve");
    else if (data === "n" || data === "N") done("reject");
    else if (matchesKey(data, Key.escape) || data === "l" || data === "L" || data === "q") done("later");
  },
  invalidate() {},
  render(width) {
    const theme = ctx.ui.theme;
    const innerWidth = Math.max(20, width - 4);
    const border = (left, fill, right) => truncateToWidth(`${left}${fill.repeat(Math.max(0, width - 2))}${right}`, width);
    const row = (content) => {
      const clipped = truncateToWidth(content, innerWidth);
      return truncateToWidth(`│ ${clipped}${" ".repeat(Math.max(0, innerWidth - visibleWidth(clipped)))} │`, width);
    };
    const title = proposal.kind === "upgrade"
      ? theme.fg("success", theme.bold("Upgrade ready"))
      : theme.fg("accent", theme.bold("Steward proposal"));
    const body = [
      proposal.title || proposal.id,
      "",
      ...(proposal.summary ? wrapTextWithAnsi(proposal.summary, innerWidth) : []),
      ...(proposal.details ? ["", ...wrapTextWithAnsi(proposal.details, innerWidth)] : []),
      ...(proposal.risk ? ["", theme.fg("warning", "Risk: ") + proposal.risk] : []),
    ];
    const lines = [border("╭", "─", "╮"), row(title), row(theme.fg("dim", proposal.id)), border("├", "─", "┤")];
    for (const line of body.slice(0, 22)) lines.push(row(line));
    lines.push(border("├", "─", "┤"));
    lines.push(row(theme.fg("success", "enter/y approve") + theme.fg("dim", " • n reject • esc/l later")));
    lines.push(border("╰", "─", "╯"));
    return lines;
  },
});

const checkStewardProposals = async (pi, ctx) => {
  if (ctx.mode !== "tui" || !ctx.hasUI) return;
  const result = await pi.exec(gtBin, ["steward", "proposal", "list", "--pending", "--json"], { cwd: ctx.cwd, timeout: 10000 }).catch(() => null);
  if (!result || result.code !== 0 || !result.stdout?.trim()) return;
  let proposals = [];
  try { proposals = JSON.parse(result.stdout); } catch { return; }
  if (!Array.isArray(proposals) || proposals.length === 0) return;
  const proposal = proposals[0];
  const action = await ctx.ui.custom((_tui, _theme, _keybindings, done) => createProposalModal(proposal, ctx, done), {
    overlay: true,
    overlayOptions: { width: "70%", minWidth: 58, maxHeight: "85%", anchor: "center", margin: 1 },
  });
  if (action === "approve") {
    const approve = await pi.exec(gtBin, ["steward", "proposal", "approve", proposal.id], { cwd: ctx.cwd, timeout: 120000 });
    ctx.ui.notify(approve.code === 0 ? `Approved ${proposal.id}` : `Approval failed: ${approve.stderr || approve.stdout}`, approve.code === 0 ? "info" : "error");
  } else if (action === "reject") {
    const reject = await pi.exec(gtBin, ["steward", "proposal", "reject", proposal.id], { cwd: ctx.cwd, timeout: 30000 });
    ctx.ui.notify(reject.code === 0 ? `Rejected ${proposal.id}` : `Reject failed: ${reject.stderr || reject.stdout}`, reject.code === 0 ? "info" : "error");
  }
};

const showHealthModal = async (pi, ctx) => {
  if (ctx.mode !== "tui") {
    const result = await pi.exec(gtBin, ["health"], { cwd: ctx.cwd, timeout: 15000 });
    ctx.ui.notify((result.stdout || result.stderr || "gt health produced no output").trim(), result.code === 0 ? "info" : "error");
    return;
  }
  let state = await loadHealth(pi, ctx.cwd);
  updateHealthStatus(ctx, state);
  await ctx.ui.custom((tui, _theme, _keybindings, done) => {
    const modal = createHealthModal(state, ctx, done);
    modal.setPi(pi);
    modal.setHandle({ requestRender: () => tui.requestRender() });
    return modal;
  }, {
    overlay: true,
    overlayOptions: {
      width: "70%",
      minWidth: 56,
      maxHeight: "85%",
      anchor: "center",
      margin: 1,
    },
  });
};

export default (pi) => {
  const role = (process.env.GT_ROLE || "").toLowerCase();
  let primeContext = null;
  let contextInjected = false;
  let lastMailCheck = 0;
  let lastProposalCheck = 0;
  let proposalCheckRunning = false;

  const isHumanCoordinationSurface = () =>
    role === "mayor" || role === "mayor/" || role === "overseer" || role === "human";

  const shouldInjectMail = () => !isHumanCoordinationSurface();

  const maybeCheckStewardProposals = async (ctx, force = false) => {
    if (!isHumanCoordinationSurface()) return;
    const now = Date.now();
    if (!force && now - lastProposalCheck < 60000) return;
    if (proposalCheckRunning) return;
    proposalCheckRunning = true;
    lastProposalCheck = now;
    try {
      await checkStewardProposals(pi, ctx);
    } finally {
      proposalCheckRunning = false;
    }
  };

  pi.registerCommand("gt-health", {
    description: "Show Gas Town health as a Pi-native modal",
    handler: async (_args, ctx) => {
      await showHealthModal(pi, ctx);
    },
  });

  // SessionStart — run gt prime and capture context for injection
  pi.on("session_start", async (event, context) => {
    if (context.hasUI) {
      loadHealth(pi, context.cwd).then((state) => updateHealthStatus(context, state));
      setTimeout(() => { maybeCheckStewardProposals(context, true); }, 500);
    }

    try {
      const result = await pi.exec(gtBin, ["prime", "--hook"]);
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
        const mailResult = await pi.exec(gtBin, ["mail", "check", "--inject"]);
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

  pi.on("agent_end", async (_event, context) => {
    await maybeCheckStewardProposals(context);
  });

  // PreToolUse equivalent — guard PR workflow operations. Direct git push is
  // intentionally allowed; refineries and workers land completed work by
  // pushing target branches, while force-push protection lives in the
  // dangerous-command guard.
  pi.on("tool_call", async (event, context) => {
    if (event.toolName === "bash" && event.input?.command) {
      const cmd = event.input.command;
      if (
        cmd.includes("gh pr create") ||
        cmd.includes("git checkout -b") ||
        cmd.includes("git switch -c")
      ) {
        try {
          const result = await pi.exec(gtBin, ["tap", "guard", "pr-workflow"]);
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
      await pi.exec(gtBin, ["costs", "record"]);
    } catch (e) {
      console.error("[gastown] gt costs record failed:", e.message);
    }
  });
};
