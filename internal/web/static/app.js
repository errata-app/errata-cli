'use strict';

// ─── Helpers ─────────────────────────────────────────────────────────────────

function fmtTokens(n) {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000)     return (n / 1_000).toFixed(1) + 'k';
  return String(n);
}

function fmtStat(resp) {
  let s = resp.latency_ms + 'ms';
  const tot = (resp.input_tokens || 0) + (resp.output_tokens || 0);
  if (tot > 0) s += '  ·  ' + fmtTokens(tot) + ' tok';
  if (resp.cost_usd > 0) s += '  ·  $' + resp.cost_usd.toFixed(4);
  if (resp.context_window_tokens > 0 && resp.input_tokens > 0) {
    s += '  ·  ~' + Math.round(resp.input_tokens / resp.context_window_tokens * 100) + '% ctx';
  }
  return s;
}

function saveHistory() {
  try {
    localStorage.setItem('errata_history', JSON.stringify(history.slice(-HISTORY_DISPLAY_CAP)));
  } catch (e) {}
}

function loadHistory() {
  try {
    const raw = localStorage.getItem('errata_history');
    if (raw) history.push(...JSON.parse(raw));
  } catch (e) {}
}

// Slim down a full server response object to what we need to store.
function slimResponse(r) {
  return {
    model_id:              r.model_id,
    latency_ms:            r.latency_ms             || 0,
    input_tokens:          r.input_tokens           || 0,
    output_tokens:         r.output_tokens          || 0,
    cost_usd:              r.cost_usd               || 0,
    context_window_tokens: r.context_window_tokens  || 0,
    error:                 r.error                  || '',
    text:                  r.text                   || '',
  };
}

function buildRunEntry(prompt, responses, selected) {
  return {
    type:      'run',
    prompt,
    ts:        Date.now(),
    responses: responses.map(slimResponse),
    selected,   // model_id string or null
  };
}

// ─── State ───────────────────────────────────────────────────────────────────

let appState         = 'idle';   // 'idle' | 'running' | 'selecting'
let activeSlashIdx   = -1;       // index of highlighted item in slash-completions
let sessionCostUSD        = 0;   // cumulative inference cost this session
let sessionCostPerModel   = {};  // per-model cumulative cost this session
let ws               = null;
let verbose          = true;
let currentResponses = null;
let currentRunPrompt = '';
let savedRunData     = null;     // {prompt, responses, selected} — awaiting 'applied'
let currentRunEl     = null;     // live DOM element for the ongoing run
let currentPanelsGrid = null;    // panels grid inside currentRunEl
let modelsData       = null;     // cached /api/available-models response
let activeModelFilter = null;    // null = all configured; string[] = per-connection filter
let disabledTools     = new Set(); // tool names currently disabled for this session

const PANEL_CAP           = 50;  // per-provider display limit when not filtering
const HISTORY_DISPLAY_CAP = 50;  // localStorage history entry limit
const PROMPT_PREVIEW_LEN  = 90;  // chars shown in run history prompt headers
const ERROR_TRUNCATE_LEN  = 100; // max chars taken from error strings

let slashCommands = []; // populated from /api/commands at init

const history = [];              // [{type:'msg'|'run', ...}]
const panels  = {};              // modelId → {el, eventsEl}

// ─── DOM refs ─────────────────────────────────────────────────────────────────

const mainEl     = document.getElementById('main');
const inputEl    = document.getElementById('prompt-input');
const btnSend    = document.getElementById('btn-send');
const btnVerbose = document.getElementById('btn-verbose');
const btnStats   = document.getElementById('btn-stats');
const btnModels  = document.getElementById('btn-models');

// ─── WebSocket ────────────────────────────────────────────────────────────────

function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(proto + '//' + location.host + '/ws');

  ws.onopen = () => {
    // Restore saved model filter from previous session.
    try {
      const saved = localStorage.getItem('errata_model_filter');
      if (saved) {
        const ids = JSON.parse(saved);
        if (Array.isArray(ids) && ids.length > 0) {
          wsSend({ type: 'set_models', model_ids: ids.map(id => ({ id, provider: '' })) });
        }
      }
    } catch (e) { /* ignore malformed storage */ }
    if (appState === 'idle') {
      btnSend.disabled = false;
      inputEl.disabled = false;
      inputEl.placeholder = 'Enter a prompt… (Shift+Enter for newline)';
      inputEl.focus();
    }
  };

  ws.onmessage = e => handleServerMessage(JSON.parse(e.data));

  ws.onclose = () => {
    ws = null;
    btnSend.disabled = true;
    inputEl.disabled = true;
    inputEl.placeholder = 'Reconnecting…';
    if (appState === 'running') {
      toIdle('Error: lost connection to server.', 'error');
    }
    setTimeout(connectWS, 2000);
  };

  ws.onerror = () => {
    // onclose fires after onerror; let it handle the reconnect.
  };
}

function wsSend(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(obj));
  }
}

// ─── Server message dispatch ──────────────────────────────────────────────────

function handleServerMessage(msg) {
  switch (msg.type) {
    case 'agent_event':
      appendPanelEvent(msg.model_id, msg.event_type, msg.data);
      break;

    case 'complete':
      if (appState === 'running') {
        msg.responses.sort((a, b) => (a.latency_ms || 0) - (b.latency_ms || 0));
        msg.responses.forEach(r => {
          const c = r.cost_usd || 0;
          sessionCostUSD += c;
          sessionCostPerModel[r.model_id] = (sessionCostPerModel[r.model_id] || 0) + c;
        });
        const hasWrites = msg.responses.some(r => r.proposed_writes && r.proposed_writes.length > 0);
        const okWithText = msg.responses.filter(r => !r.error && r.text).length;
        // Update live panel headers with the final latency/token/cost stats.
        msg.responses.forEach(resp => {
          const p = panels[resp.model_id];
          if (!p) return;
          const hdr = p.el.querySelector('.panel-header');
          if (!hdr) return;
          if (resp.error) {
            hdr.textContent = resp.model_id + '  —  error';
          } else {
            hdr.textContent = resp.model_id + '  ·  ' + fmtStat(resp);
          }
        });

        if (!hasWrites && okWithText === 0) {
          // No usable text response — go idle immediately.
          finalizeRunEl();
          history.push(buildRunEntry(currentRunPrompt, msg.responses, null));
          saveHistory();
          toIdle();
        } else if (!hasWrites && okWithText === 1) {
          // Single response — show thumbs-up/down rating bar.
          toRating(msg.responses);
        } else {
          toSelecting(msg.responses);
        }
      }
      break;

    case 'applied':
      if (savedRunData) {
        history.push(buildRunEntry(savedRunData.prompt, savedRunData.responses, savedRunData.selected));
        saveHistory();
        // Replace the content with an applied note, then collapse.
        if (currentRunEl) {
          const content = currentRunEl.querySelector('.history-run-content');
          if (content) {
            content.innerHTML = '';
            const noteText = (msg.applied && msg.applied.length > 0)
              ? '✓ Applied: ' + msg.applied.join(', ')
              : savedRunData && savedRunData.selected
                ? '✓ Voted for: ' + savedRunData.selected
                : null;
            if (noteText) {
              const note = document.createElement('div');
              note.className = 'run-applied-note';
              note.textContent = noteText;
              content.appendChild(note);
            }
            content.classList.remove('visible');
          }
          const toggle = currentRunEl.querySelector('.run-toggle');
          if (toggle) toggle.textContent = '▶';
          currentRunEl = null;
          currentPanelsGrid = null;
        }
        savedRunData = null;
      }
      toIdle();
      break;

    case 'rated':
      // Server confirmed a bad rating was recorded.
      finishRating('👎 Rated bad: ' + (msg.model_id || ''));
      break;

    case 'models_set': {
      activeModelFilter = (msg.models && msg.models.length > 0) ? [...msg.models] : null;
      if (activeModelFilter) {
        localStorage.setItem('errata_model_filter', JSON.stringify(activeModelFilter));
      } else {
        localStorage.removeItem('errata_model_filter');
      }
      const active = activeModelFilter
        ? 'Active models: ' + activeModelFilter.join(', ')
        : 'Active models: all';
      history.push({ type: 'msg', text: active, cls: '' });
      saveHistory();
      appendHistoryMsg(active, '');
      break;
    }

    case 'tools_set': {
      // msg.models contains the active (enabled) tool names.
      const allToolNames = msg.models || [];
      // Recompute disabledTools from the inverse (server sends active list).
      // We keep our disabledTools state in sync via set_tools requests, so just confirm.
      const msg2 = allToolNames.length > 0
        ? 'Active tools: ' + allToolNames.join(', ')
        : 'All tools disabled.';
      appendHistoryMsg(msg2, '');
      break;
    }

    case 'compact_complete':
      appendHistoryMsg('History compacted.', '');
      break;

    case 'history_cleared':
      appendHistoryMsg('Conversation history cleared.', '');
      break;

    case 'cancelled':
      savedRunData = null;
      toIdle('Cancelled.');
      break;

    case 'error':
      savedRunData = null;
      toIdle('Error: ' + msg.message, 'error');
      break;
  }
}

// ─── Init ─────────────────────────────────────────────────────────────────────

async function loadCommands() {
  try {
    const res = await fetch('/api/commands');
    slashCommands = await res.json();
  } catch (e) {
    console.warn('Could not load commands:', e);
  }
}

function init() {
  loadCommands();
  loadHistory();
  btnSend.addEventListener('click', handleSend);
  inputEl.addEventListener('input', updateSlashCompletions);
  inputEl.addEventListener('keydown', e => {
    const completionsEl = document.getElementById('slash-completions');
    const isOpen = completionsEl.classList.contains('visible');

    if (isOpen) {
      const typed = inputEl.value.split(' ')[0].toLowerCase();
      const matches = slashCommands.filter(c => c.name.startsWith(typed));

      if (e.key === 'ArrowUp') {
        e.preventDefault();
        activeSlashIdx = (activeSlashIdx <= 0 ? matches.length : activeSlashIdx) - 1;
        updateSlashCompletions();
        return;
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        activeSlashIdx = (activeSlashIdx + 1) % matches.length;
        updateSlashCompletions();
        return;
      }
      if (e.key === 'Tab') {
        e.preventDefault();
        const idx = activeSlashIdx >= 0 ? activeSlashIdx : 0;
        if (matches[idx]) {
          inputEl.value = matches[idx].name + ' ';
          inputEl.dispatchEvent(new Event('input'));
        }
        return;
      }
      if (e.key === 'Enter' && activeSlashIdx >= 0) {
        e.preventDefault();
        if (matches[activeSlashIdx]) {
          inputEl.value = matches[activeSlashIdx].name + ' ';
          inputEl.dispatchEvent(new Event('input'));
        }
        return;
      }
      if (e.key === 'Escape') {
        hideSlashCompletions();
        return;
      }
    }

    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
  });
  btnVerbose.classList.add('active');
  btnVerbose.addEventListener('click', () => {
    verbose = !verbose;
    btnVerbose.classList.toggle('active', verbose);
  });
  btnStats.addEventListener('click', showStats);
  btnModels.addEventListener('click', openModelsPanel);
  document.getElementById('btn-models-close').addEventListener('click', closeModelsPanel);
  document.getElementById('models-backdrop').addEventListener('click', closeModelsPanel);
  document.getElementById('btn-models-apply').addEventListener('click', applyModels);
  document.getElementById('models-search').addEventListener('input', e => {
    if (modelsData) renderModelsPanel(e.target.value.trim().toLowerCase());
  });
  connectWS();
  renderIdle();
}

// ─── State transitions ────────────────────────────────────────────────────────

// Removes running chrome (status indicator + cancel button) from currentRunEl.
function finalizeRunEl() {
  if (!currentRunEl) return;
  const statusEl = currentRunEl.querySelector('.running-status');
  if (statusEl) statusEl.remove();
  const cancelArea = currentRunEl.querySelector('.cancel-area');
  if (cancelArea) cancelArea.remove();
  currentRunEl = null;
  currentPanelsGrid = null;
}

function toRunning() {
  appState = 'running';
  btnSend.disabled = true;
  inputEl.disabled = true;
  Object.keys(panels).forEach(k => delete panels[k]);

  // Build a live run entry and append to chat — never wipe mainEl.
  const entry = document.createElement('div');
  entry.className = 'history-run';

  // Header: toggle ▼ | prompt preview | running… (right-aligned)
  const header = document.createElement('div');
  header.className = 'history-run-header';

  const toggle = document.createElement('span');
  toggle.className = 'run-toggle';
  toggle.textContent = '▼';

  const promptEl = document.createElement('span');
  promptEl.className = 'run-prompt';
  const preview = currentRunPrompt.replace(/\n/g, ' ');
  promptEl.textContent = preview.length > PROMPT_PREVIEW_LEN ? preview.slice(0, PROMPT_PREVIEW_LEN) + '…' : preview;

  const statusEl = document.createElement('span');
  statusEl.className = 'running-status';
  statusEl.textContent = 'running…';

  header.appendChild(toggle);
  header.appendChild(promptEl);
  header.appendChild(statusEl);
  entry.appendChild(header);

  // Content — open by default while running.
  const content = document.createElement('div');
  content.className = 'history-run-content visible';

  const grid = document.createElement('div');
  grid.className = 'panels history-run-grid';
  content.appendChild(grid);
  currentPanelsGrid = grid;

  const cancelArea = document.createElement('div');
  cancelArea.className = 'cancel-area';
  const cancelBtn = document.createElement('button');
  cancelBtn.className = 'btn-cancel';
  cancelBtn.textContent = 'Cancel';
  cancelBtn.addEventListener('click', () => wsSend({ type: 'cancel' }));
  cancelArea.appendChild(cancelBtn);
  content.appendChild(cancelArea);

  entry.appendChild(content);

  // Toggle content on header click.
  header.addEventListener('click', () => {
    const open = content.classList.toggle('visible');
    toggle.textContent = open ? '▼' : '▶';
  });

  mainEl.appendChild(entry);
  currentRunEl = entry;
  mainEl.scrollTop = mainEl.scrollHeight;
}

function toSelecting(responses) {
  appState = 'selecting';
  currentResponses = responses;
  btnSend.disabled = false;
  inputEl.disabled = false;

  if (!currentRunEl) return;

  // Remove running chrome from header.
  const statusEl = currentRunEl.querySelector('.running-status');
  if (statusEl) statusEl.remove();
  const cancelArea = currentRunEl.querySelector('.cancel-area');
  if (cancelArea) cancelArea.remove();

  // Grid is gone once content is cleared; release the reference.
  currentPanelsGrid = null;

  // Replace the panels grid with the diff + selection UI in place.
  const content = currentRunEl.querySelector('.history-run-content');
  if (content) {
    content.innerHTML = '';
    buildSelectingContent(responses, content);
  }

  mainEl.scrollTop = mainEl.scrollHeight;
}

function toRating(responses) {
  appState = 'rating';
  currentResponses = responses;
  btnSend.disabled = false;
  inputEl.disabled = false;

  if (!currentRunEl) return;

  const statusEl = currentRunEl.querySelector('.running-status');
  if (statusEl) statusEl.remove();
  const cancelArea = currentRunEl.querySelector('.cancel-area');
  if (cancelArea) cancelArea.remove();
  currentPanelsGrid = null;

  const content = currentRunEl.querySelector('.history-run-content');
  if (!content) return;
  content.innerHTML = '';

  // Show the single response text.
  const resp = responses.find(r => !r.error && r.text);
  if (resp) {
    const textEl = document.createElement('div');
    textEl.className = 'response-text';
    textEl.textContent = resp.text;
    content.appendChild(textEl);
  }

  // Rating bar.
  const bar = document.createElement('div');
  bar.className = 'rating-bar';

  const label = document.createElement('span');
  label.className = 'rating-label';
  label.textContent = 'Rate this response:';
  bar.appendChild(label);

  const goodBtn = document.createElement('button');
  goodBtn.className = 'btn-rate btn-rate-good';
  goodBtn.textContent = '👍 Good';
  goodBtn.addEventListener('click', () => doRate(resp ? resp.model_id : null, true));
  bar.appendChild(goodBtn);

  const badBtn = document.createElement('button');
  badBtn.className = 'btn-rate btn-rate-bad';
  badBtn.textContent = '👎 Bad';
  badBtn.addEventListener('click', () => doRate(resp ? resp.model_id : null, false));
  bar.appendChild(badBtn);

  const skipBtn = document.createElement('button');
  skipBtn.className = 'btn-rate btn-rate-skip';
  skipBtn.textContent = 'Skip';
  skipBtn.addEventListener('click', () => doRate(null, null));
  bar.appendChild(skipBtn);

  content.appendChild(bar);
  mainEl.scrollTop = mainEl.scrollHeight;
}

function doRate(modelId, good) {
  if (appState !== 'rating') return;
  if (good === true && modelId) {
    // Thumbs-up — record as a preference win via existing select path.
    savedRunData = { prompt: currentRunPrompt, responses: currentResponses, selected: modelId };
    appState = 'idle';
    wsSend({ type: 'select', model_id: modelId });
  } else if (good === false && modelId) {
    // Thumbs-down — send to server for bad-rating recording; await 'rated' response.
    savedRunData = { prompt: currentRunPrompt, responses: currentResponses, selected: null };
    appState = 'idle';
    wsSend({ type: 'rate_bad', model_id: modelId });
  } else {
    // Skip — no server call, just clean up.
    finishRating('Skipped.');
  }
}

function finishRating(noteText) {
  if (currentRunEl) {
    const content = currentRunEl.querySelector('.history-run-content');
    if (content) {
      content.innerHTML = '';
      const note = document.createElement('div');
      note.className = 'run-applied-note';
      note.textContent = noteText;
      content.appendChild(note);
      content.classList.remove('visible');
      const toggle = currentRunEl.querySelector('.run-toggle');
      if (toggle) toggle.textContent = '▶';
    }
    currentRunEl = null;
    currentPanelsGrid = null;
  }
  history.push(buildRunEntry(currentRunPrompt, currentResponses, null));
  saveHistory();
  currentResponses = null;
  savedRunData = null;
  toIdle();
}

// Finalizes the live run entry and optionally appends a status message.
function toIdle(msg, cls) {
  appState         = 'idle';
  currentResponses = null;
  const connected = ws && ws.readyState === WebSocket.OPEN;
  btnSend.disabled = !connected;
  inputEl.disabled = !connected;

  // Collapse and clean up the live run entry if present.
  if (currentRunEl) {
    const statusEl = currentRunEl.querySelector('.running-status');
    if (statusEl) statusEl.remove();
    const cancelArea = currentRunEl.querySelector('.cancel-area');
    if (cancelArea) cancelArea.remove();
    const content = currentRunEl.querySelector('.history-run-content');
    if (content) content.classList.remove('visible');
    const toggle = currentRunEl.querySelector('.run-toggle');
    if (toggle) toggle.textContent = '▶';
    currentRunEl = null;
    currentPanelsGrid = null;
  }

  if (msg) {
    history.push({ type: 'msg', text: msg, cls: cls || '' });
    saveHistory();
    appendHistoryMsg(msg, cls || '');
  }

  inputEl.focus();
}

// Appends a single status message to mainEl without re-rendering history.
function appendHistoryMsg(text, cls) {
  const div = document.createElement('div');
  div.className = 'history-item' + (cls ? ' ' + cls : '');
  div.textContent = text;
  mainEl.appendChild(div);
}

// ─── Render: idle ─────────────────────────────────────────────────────────────

function renderRunEntry(entry) {
  const div = document.createElement('div');
  div.className = 'history-run';

  const header = document.createElement('div');
  header.className = 'history-run-header';

  const toggle = document.createElement('span');
  toggle.className = 'run-toggle';
  toggle.textContent = '▶';

  const promptEl = document.createElement('span');
  promptEl.className = 'run-prompt';
  const preview = entry.prompt.replace(/\n/g, ' ');
  promptEl.textContent = preview.length > PROMPT_PREVIEW_LEN ? preview.slice(0, PROMPT_PREVIEW_LEN) + '…' : preview;

  header.appendChild(toggle);
  header.appendChild(promptEl);
  div.appendChild(header);

  // Content — hidden by default in history.
  const content = document.createElement('div');
  content.className = 'history-run-content';

  const grid = document.createElement('div');
  grid.className = 'panels history-run-grid';

  entry.responses.forEach((resp, idx) => {
    const isErr = !!resp.error;
    const color = isErr ? 'var(--diff-remove)' : PANEL_COLORS[idx % PANEL_COLORS.length];

    const panel = document.createElement('div');
    panel.className = 'panel' + (entry.selected === resp.model_id ? ' panel-selected' : '');
    panel.style.borderLeftColor = color;

    const hdr = document.createElement('div');
    hdr.className = 'panel-header';
    hdr.style.color = color;

    if (isErr) {
      hdr.textContent = resp.model_id + '  —  error';
    } else {
      hdr.textContent = resp.model_id + '  ·  ' + fmtStat(resp);
    }
    panel.appendChild(hdr);

    const body = document.createElement('div');
    body.className = 'panel-events';

    if (isErr) {
      const line = document.createElement('div');
      line.className = 'panel-event error';
      const lbl = document.createElement('span');
      lbl.className = 'panel-event-label';
      lbl.textContent = 'error';
      const dat = document.createElement('span');
      dat.className = 'panel-event-data';
      dat.textContent = resp.error.split('\n')[0].slice(0, ERROR_TRUNCATE_LEN);
      line.appendChild(lbl);
      line.appendChild(dat);
      body.appendChild(line);
    } else if (resp.text) {
      const textEl = document.createElement('div');
      textEl.className = 'panel-text-response';
      textEl.textContent = resp.text;
      body.appendChild(textEl);
    }

    panel.appendChild(body);
    grid.appendChild(panel);
  });

  content.appendChild(grid);

  if (entry.selected) {
    const note = document.createElement('div');
    note.className = 'run-applied-note';
    note.textContent = '✓ Applied: ' + entry.selected;
    content.appendChild(note);
  }

  div.appendChild(content);

  header.addEventListener('click', () => {
    const open = content.classList.toggle('visible');
    toggle.textContent = open ? '▼' : '▶';
  });

  return div;
}

// Called only at startup and after /clear.
function renderIdle() {
  const frag = document.createDocumentFragment();
  const start = Math.max(0, history.length - 20);
  for (let i = start; i < history.length; i++) {
    const entry = history[i];
    if (entry.type === 'run') {
      frag.appendChild(renderRunEntry(entry));
    } else {
      const div = document.createElement('div');
      div.className = 'history-item' + (entry.cls ? ' ' + entry.cls : '');
      div.textContent = entry.text || '';
      frag.appendChild(div);
    }
  }
  mainEl.innerHTML = '';
  mainEl.appendChild(frag);
  inputEl.focus();
}

// ─── Render: running ─────────────────────────────────────────────────────────

const PANEL_COLORS = ['var(--accent-0)', 'var(--accent-1)', 'var(--accent-2)'];

function getOrCreatePanel(modelId) {
  if (panels[modelId]) return panels[modelId];
  const grid = currentPanelsGrid;
  if (!grid) return null;

  const idx = Object.keys(panels).length;
  const color = PANEL_COLORS[idx % PANEL_COLORS.length];

  const panel = document.createElement('div');
  panel.className = 'panel';
  panel.style.borderLeftColor = color;

  const header = document.createElement('div');
  header.className = 'panel-header';
  header.style.color = color;
  header.textContent = modelId;
  panel.appendChild(header);

  const eventsEl = document.createElement('div');
  eventsEl.className = 'panel-events';
  panel.appendChild(eventsEl);

  grid.appendChild(panel);
  panels[modelId] = { el: panel, eventsEl };
  return panels[modelId];
}

function appendPanelEvent(modelId, type, data) {
  const p = getOrCreatePanel(modelId);
  if (!p) return;

  const line = document.createElement('div');
  line.className = 'panel-event ' + (type || 'text');

  const label = document.createElement('span');
  label.className = 'panel-event-label';
  label.textContent = type || '';

  const dataSpan = document.createElement('span');
  dataSpan.className = 'panel-event-data';
  dataSpan.textContent = data || '';

  line.appendChild(label);
  line.appendChild(dataSpan);
  p.eventsEl.appendChild(line);
  p.el.scrollTop = p.el.scrollHeight;
}

// ─── Render: selecting ───────────────────────────────────────────────────────

// Populates container with diff sections + selection buttons (in-place in currentRunEl).
function buildSelectingContent(responses, container) {
  const anyWrites = responses.some(r => !r.error && r.proposed_writes && r.proposed_writes.length > 0);

  responses.forEach((resp, idx) => {
    if (resp.error) return;
    const color = PANEL_COLORS[idx % PANEL_COLORS.length];

    const section = document.createElement('div');
    section.className = 'diff-section';

    const hdr = document.createElement('div');
    hdr.className = 'diff-model-header';
    hdr.style.color = color;
    hdr.textContent = `── ${resp.model_id}  ${fmtStat(resp)}`;
    section.appendChild(hdr);

    if (!resp.proposed_writes || resp.proposed_writes.length === 0) {
      if (resp.text) {
        const textEl = document.createElement('div');
        textEl.className = 'response-text';
        textEl.textContent = resp.text;
        section.appendChild(textEl);
      }
    } else {
      resp.proposed_writes.forEach(pw => {
        const fileHdr = document.createElement('div');
        fileHdr.className = 'diff-file-header';
        fileHdr.textContent = pw.diff.is_new
          ? `    ${pw.path}  (new file)`
          : `    ${pw.path}  +${pw.diff.adds} -${pw.diff.removes}`;
        section.appendChild(fileHdr);

        const linesEl = document.createElement('div');
        linesEl.className = 'diff-lines';
        (pw.diff.lines || []).forEach(line => {
          const span = document.createElement('span');
          span.className = 'diff-line ' + line.kind;
          if (line.spans && line.spans.length > 0) {
            // Word-level highlighting: prefix char + highlighted spans.
            span.appendChild(document.createTextNode(line.content.charAt(0)));
            line.spans.forEach(ws => {
              if (ws.changed) {
                const hl = document.createElement('span');
                hl.className = line.kind === 'add' ? 'diff-word-add' : 'diff-word-remove';
                hl.textContent = ws.text;
                span.appendChild(hl);
              } else {
                span.appendChild(document.createTextNode(ws.text));
              }
            });
          } else {
            span.textContent = line.content;
          }
          linesEl.appendChild(span);
          linesEl.appendChild(document.createElement('br'));
        });
        section.appendChild(linesEl);

        if (pw.diff.truncated > 0) {
          const trunc = document.createElement('div');
          trunc.className = 'diff-truncated';
          trunc.textContent = `… ${pw.diff.truncated} more lines`;
          section.appendChild(trunc);
        }
      });
    }

    container.appendChild(section);
  });

  // Selection menu
  const menu = document.createElement('div');
  menu.className = 'selection-menu';

  const title = document.createElement('h3');
  title.textContent = anyWrites ? 'Select a response to apply:' : 'Vote for a response:';
  menu.appendChild(title);

  const buttons = document.createElement('div');
  buttons.className = 'selection-buttons';

  let selIdx = 0;
  responses.forEach((resp) => {
    if (resp.error) return;
    selIdx++;
    const btn = document.createElement('button');
    btn.className = 'btn-select';
    const cost = resp.cost_usd > 0 ? `  $${resp.cost_usd.toFixed(4)}` : '';
    if (anyWrites) {
      const files = (resp.proposed_writes || []).map(w => w.path).join(', ') || '(no writes)';
      btn.textContent = `${selIdx}  ${resp.model_id}  (${resp.latency_ms}ms${cost})  →  ${files}`;
    } else {
      btn.textContent = `${selIdx}  ${resp.model_id}  (${resp.latency_ms}ms${cost})`;
    }
    btn.addEventListener('click', () => doSelect(resp.model_id));
    buttons.appendChild(btn);
  });

  const skipBtn = document.createElement('button');
  skipBtn.className = 'btn-select btn-skip';
  skipBtn.textContent = 's  Skip';
  skipBtn.addEventListener('click', () => {
    if (currentResponses) {
      history.push(buildRunEntry(currentRunPrompt, currentResponses, null));
      saveHistory();
    }
    toIdle();
  });
  buttons.appendChild(skipBtn);

  menu.appendChild(buttons);
  container.appendChild(menu);
}

// ─── Slash completions ────────────────────────────────────────────────────────

function updateSlashCompletions() {
  const val = inputEl.value;
  const completionsEl = document.getElementById('slash-completions');

  if (appState !== 'idle' || !val.startsWith('/') || val.includes('\n')) {
    completionsEl.classList.remove('visible');
    completionsEl.innerHTML = '';
    activeSlashIdx = -1;
    return;
  }

  const typed = val.split(' ')[0].toLowerCase();
  const matches = slashCommands.filter(c => c.name.startsWith(typed));

  if (matches.length === 0) {
    completionsEl.classList.remove('visible');
    completionsEl.innerHTML = '';
    activeSlashIdx = -1;
    return;
  }

  activeSlashIdx = Math.min(activeSlashIdx, matches.length - 1);
  completionsEl.innerHTML = '';
  matches.forEach((cmd, i) => {
    const row = document.createElement('div');
    row.className = 'slash-item' + (i === activeSlashIdx ? ' active' : '');
    row.innerHTML = `<span class="slash-item-name">${cmd.name}</span><span class="slash-item-desc">${cmd.desc}</span>`;
    row.addEventListener('mousedown', e => {
      e.preventDefault(); // don't blur textarea
      inputEl.value = cmd.name + ' ';
      inputEl.focus();
      completionsEl.classList.remove('visible');
      completionsEl.innerHTML = '';
      activeSlashIdx = -1;
    });
    completionsEl.appendChild(row);
  });
  completionsEl.classList.add('visible');
}

function hideSlashCompletions() {
  const completionsEl = document.getElementById('slash-completions');
  completionsEl.classList.remove('visible');
  completionsEl.innerHTML = '';
  activeSlashIdx = -1;
}

// ─── Actions ──────────────────────────────────────────────────────────────────

// handleLocalCommand completes a client-side slash command that produces a
// simple text response: clears the input, records the message in history,
// persists it, and appends it to the feed.
function handleLocalCommand(text) {
  inputEl.value = '';
  history.push({ type: 'msg', text, cls: '' });
  saveHistory();
  appendHistoryMsg(text, '');
}

function handleSend() {
  if (appState !== 'idle') return;
  hideSlashCompletions();
  const prompt = inputEl.value.trim();
  if (!prompt) return;

  // Handle /help slash command client-side.
  if (/^\/help$/i.test(prompt)) {
    const lines = ['Commands:', ...slashCommands.map(c => `  ${c.name.padEnd(12)}  ${c.desc}`)];
    handleLocalCommand(lines.join('\n'));
    return;
  }

  // Handle /verbose slash command client-side.
  if (/^\/verbose$/i.test(prompt)) {
    verbose = !verbose;
    btnVerbose.classList.toggle('active', verbose);
    handleLocalCommand(`Verbose mode ${verbose ? 'on' : 'off'}`);
    return;
  }

  // Handle /clear slash command client-side.
  if (/^\/clear$/i.test(prompt)) {
    inputEl.value = '';
    history.length = 0;
    saveHistory();
    renderIdle();
    wsSend({ type: 'clear_history' });
    return;
  }

  // Handle /model slash command client-side.
  if (/^\/model(\s|$)/i.test(prompt)) {
    inputEl.value = '';
    const args = prompt.slice(6).trim();
    const ids = args ? args.split(/\s+/) : [];
    wsSend({ type: 'set_models', model_ids: ids.map(id => ({ id, provider: '' })) });
    return;
  }

  // Handle /compact slash command.
  if (/^\/compact$/i.test(prompt)) {
    inputEl.value = '';
    appendHistoryMsg('Compacting conversation history…', '');
    wsSend({ type: 'compact' });
    return;
  }

  // Handle /models slash command.
  if (/^\/models$/i.test(prompt)) {
    inputEl.value = '';
    openModelsPanel();
    return;
  }

  // Handle /stats slash command.
  if (/^\/stats$/i.test(prompt)) {
    inputEl.value = '';
    showStats();
    return;
  }

  // Handle /totalcost slash command.
  if (/^\/totalcost$/i.test(prompt)) {
    handleLocalCommand(`Total session cost: $${sessionCostUSD.toFixed(4)}`);
    return;
  }

  // Handle /tools slash command.
  if (/^\/tools(\s|$)/i.test(prompt)) {
    inputEl.value = '';
    const args = prompt.slice(6).trim().toLowerCase();
    const parts = args.split(/\s+/).filter(Boolean);

    if (args === '' || args === 'reset') {
      if (args === 'reset') disabledTools.clear();
      wsSend({ type: 'set_tools', disabled: [...disabledTools] });
      return;
    }
    if ((parts[0] === 'on' || parts[0] === 'off') && parts.length > 1) {
      const action = parts[0];
      const names = parts.slice(1);
      names.forEach(n => {
        if (action === 'off') disabledTools.add(n);
        else disabledTools.delete(n);
      });
      wsSend({ type: 'set_tools', disabled: [...disabledTools] });
      return;
    }
    // Unknown /tools sub-command — show help inline.
    handleLocalCommand('Usage: /tools  |  /tools off <name...>  |  /tools on <name...>  |  /tools reset');
    return;
  }

  currentRunPrompt = prompt;
  inputEl.value = '';
  toRunning();

  wsSend({ type: 'run', prompt, verbose });
}

function doSelect(modelId) {
  if (appState !== 'selecting') return;
  savedRunData = { prompt: currentRunPrompt, responses: currentResponses, selected: modelId };
  currentResponses = null;
  appState = 'idle'; // prevent double-selection while awaiting 'applied'
  wsSend({ type: 'select', model_id: modelId });
}

// ─── Nav actions ──────────────────────────────────────────────────────────────

async function showStats() {
  try {
    const res = await fetch('/api/stats');
    const { tally } = await res.json();
    const tallyEntries = Object.entries(tally || {});
    const lines = ['Stats:'];

    if (tallyEntries.length === 0) {
      lines.push('  No preference data yet.');
    } else {
      lines.push('  Preference wins:');
      tallyEntries
        .sort((a, b) => b[1] - a[1])
        .forEach(([m, n]) => lines.push(`    ${m}: ${n} win${n !== 1 ? 's' : ''}`));
    }

    const costEntries = Object.entries(sessionCostPerModel).filter(([, c]) => c > 0);
    if (costEntries.length > 0) {
      lines.push('  Session cost:');
      costEntries
        .sort((a, b) => b[1] - a[1])
        .forEach(([m, c]) => lines.push(`    ${m}: $${c.toFixed(4)}`));
      lines.push(`  Total: $${sessionCostUSD.toFixed(4)}`);
    }

    const text = lines.join('\n');
    history.push({ type: 'msg', text, cls: '' });
    saveHistory();
    appendHistoryMsg(text, '');
  } catch (err) {
    console.error('stats:', err);
  }
}

function fmtPrice(v) {
  if (!v) return null;
  return '$' + (v >= 1 ? v.toFixed(2) : parseFloat(v.toPrecision(3)));
}

function openModelsPanel() {
  modelsData = null;
  document.getElementById('models-backdrop').classList.add('open');
  document.getElementById('models-panel').classList.add('open');
  document.getElementById('models-search').value = '';
  document.getElementById('models-panel-body').textContent = 'Loading…';
  fetch('/api/available-models')
    .then(r => r.json())
    .then(data => { modelsData = data; renderModelsPanel(''); })
    .catch(() => { document.getElementById('models-panel-body').textContent = 'Error loading models.'; });
}

function closeModelsPanel() {
  document.getElementById('models-backdrop').classList.remove('open');
  document.getElementById('models-panel').classList.remove('open');
}

function renderModelsPanel(filter) {
  const body = document.getElementById('models-panel-body');
  body.innerHTML = '';
  if (!modelsData) return;

  const activeSet = new Set(
    activeModelFilter !== null ? activeModelFilter : (modelsData.active || [])
  );

  let anyVisible = false;
  for (const p of (modelsData.providers || [])) {
    const section = document.createElement('div');

    const hdr = document.createElement('div');
    hdr.className = 'models-provider-header';

    if (p.error) {
      hdr.textContent = p.name + ' — error';
      section.appendChild(hdr);
      body.appendChild(section);
      anyVisible = true;
      continue;
    }

    hdr.textContent = (p.total_count > p.count)
      ? `${p.name} (${p.count} of ${p.total_count}, chat only)`
      : `${p.name} (${p.count})`;

    const models = p.models || [];
    const visible = filter ? models.filter(m => m.id.toLowerCase().includes(filter)) : models;
    if (visible.length === 0 && filter) continue;

    const over = !filter && visible.length > PANEL_CAP;
    const shown = over ? visible.slice(0, PANEL_CAP) : visible;

    section.appendChild(hdr);

    for (const m of shown) {
      const item = document.createElement('div');
      item.className = 'models-item';

      const lbl = document.createElement('label');

      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = m.id;
      cb.dataset.provider = p.name;
      cb.checked = activeSet.has(m.id);

      const idSpan = document.createElement('span');
      idSpan.className = 'models-item-id';
      idSpan.textContent = m.id;

      lbl.appendChild(cb);
      lbl.appendChild(idSpan);

      if (m.input_pmt) {
        const priceSpan = document.createElement('span');
        priceSpan.className = 'models-item-price';
        priceSpan.textContent = `${fmtPrice(m.input_pmt)} in / ${fmtPrice(m.output_pmt)} out /1M`;
        lbl.appendChild(priceSpan);
      }

      item.appendChild(lbl);
      section.appendChild(item);
    }

    if (over) {
      const more = document.createElement('div');
      more.className = 'models-more';
      more.textContent = `… and ${visible.length - PANEL_CAP} more (type to filter)`;
      section.appendChild(more);
    }

    body.appendChild(section);
    anyVisible = true;
  }

  if (!anyVisible && filter) {
    const none = document.createElement('div');
    none.className = 'models-more';
    none.textContent = `No models match "${filter}"`;
    body.appendChild(none);
  }
}

function applyModels() {
  const specs = [...document.querySelectorAll('#models-panel-body input[type=checkbox]:checked')]
    .map(cb => ({ id: cb.value, provider: cb.dataset.provider || '' }));
  wsSend({ type: 'set_models', model_ids: specs });
  closeModelsPanel();
}

// ─── Start ────────────────────────────────────────────────────────────────────

init();
