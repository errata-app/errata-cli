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
    localStorage.setItem('errata_history', JSON.stringify(history.slice(-50)));
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
let ws               = null;
let verbose          = true;
let currentResponses = null;
let currentRunPrompt = '';
let savedRunData     = null;     // {prompt, responses, selected} — awaiting 'applied'
let currentRunEl     = null;     // live DOM element for the ongoing run
let currentPanelsGrid = null;    // panels grid inside currentRunEl

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

  ws.onmessage = e => handleServerMessage(JSON.parse(e.data));

  ws.onclose = () => {
    ws = null;
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
        const hasWrites = msg.responses.some(r => r.proposed_writes && r.proposed_writes.length > 0);
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

        if (!hasWrites) {
          // Leave panels visible; remove running chrome, then reset state via toIdle().
          finalizeRunEl();
          history.push(buildRunEntry(currentRunPrompt, msg.responses, null));
          saveHistory();
          toIdle();
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
            if (msg.applied && msg.applied.length > 0) {
              const note = document.createElement('div');
              note.className = 'run-applied-note';
              note.textContent = '✓ Applied: ' + msg.applied.join(', ');
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

    case 'models_set': {
      const active = msg.models && msg.models.length > 0
        ? 'Active models: ' + msg.models.join(', ')
        : 'Active models: all';
      history.push({ type: 'msg', text: active, cls: '' });
      saveHistory();
      appendHistoryMsg(active, '');
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

function init() {
  loadHistory();
  btnSend.addEventListener('click', handleSend);
  inputEl.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
  });
  btnVerbose.classList.add('active');
  btnVerbose.addEventListener('click', () => {
    verbose = !verbose;
    btnVerbose.classList.toggle('active', verbose);
  });
  btnStats.addEventListener('click', showStats);
  btnModels.addEventListener('click', showModels);
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
  promptEl.textContent = preview.length > 90 ? preview.slice(0, 90) + '…' : preview;

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

// Finalizes the live run entry and optionally appends a status message.
function toIdle(msg, cls) {
  appState         = 'idle';
  currentResponses = null;
  btnSend.disabled = false;
  inputEl.disabled = false;

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
  promptEl.textContent = preview.length > 90 ? preview.slice(0, 90) + '…' : preview;

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
      dat.textContent = resp.error.split('\n')[0].slice(0, 100);
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
      const noW = document.createElement('div');
      noW.className = 'no-writes';
      noW.textContent = '(no file writes proposed)';
      section.appendChild(noW);
      if (resp.text) {
        const preview = document.createElement('div');
        preview.className = 'text-preview';
        preview.textContent = resp.text.split('\n')[0].slice(0, 100);
        section.appendChild(preview);
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
          span.textContent = line.content;
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
  title.textContent = 'Select a response to apply:';
  menu.appendChild(title);

  const buttons = document.createElement('div');
  buttons.className = 'selection-buttons';

  let selIdx = 0;
  responses.forEach((resp) => {
    if (resp.error) return;
    selIdx++;
    const files = (resp.proposed_writes || []).map(w => w.path).join(', ') || '(no writes)';
    const btn = document.createElement('button');
    btn.className = 'btn-select';
    const cost = resp.cost_usd > 0 ? `  $${resp.cost_usd.toFixed(4)}` : '';
    btn.textContent = `${selIdx}  ${resp.model_id}  (${resp.latency_ms}ms${cost})  →  ${files}`;
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

// ─── Actions ──────────────────────────────────────────────────────────────────

function handleSend() {
  if (appState !== 'idle') return;
  const prompt = inputEl.value.trim();
  if (!prompt) return;

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
    wsSend({ type: 'set_models', model_ids: ids });
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
    showModels();
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
    const entries = Object.entries(tally || {});
    const text = entries.length === 0
      ? 'Stats: no preference data yet.'
      : 'Stats:\n' + entries.map(([m, n]) => `  ${m}: ${n} wins`).join('\n');
    history.push({ type: 'msg', text, cls: '' });
    saveHistory();
    appendHistoryMsg(text, '');
  } catch (err) {
    console.error('stats:', err);
  }
}

const MODEL_LIST_CAP = 50;

function fmtPrice(v) {
  if (!v) return null;
  return '$' + (v >= 1 ? v.toFixed(2) : parseFloat(v.toPrecision(3)));
}

async function showModels() {
  try {
    const res = await fetch('/api/available-models');
    const data = await res.json();

    const parts = ['Active: ' + (data.active || []).join(', ')];

    for (const p of (data.providers || [])) {
      if (p.error) {
        parts.push(`${p.name} — error: ${p.error}`);
        continue;
      }
      const header = (p.total_count > p.count)
        ? `${p.name} (${p.count} of ${p.total_count}, chat only)`
        : `${p.name} (${p.count})`;
      if (p.count > MODEL_LIST_CAP) {
        parts.push(`${header} — set ERRATA_ACTIVE_MODELS=<id> to use one`);
      } else {
        const lines = (p.models || []).map(m => {
          const inP = fmtPrice(m.input_pmt), outP = fmtPrice(m.output_pmt);
          return inP ? `  ${m.id}  (${inP} in / ${outP} out /1M)` : `  ${m.id}`;
        });
        parts.push(`${header}:\n${lines.join('\n')}`);
      }
    }

    const text = parts.join('\n\n');
    history.push({ type: 'msg', text, cls: '' });
    saveHistory();
    appendHistoryMsg(text, '');
  } catch (err) {
    console.error('models:', err);
  }
}

// ─── Start ────────────────────────────────────────────────────────────────────

init();
