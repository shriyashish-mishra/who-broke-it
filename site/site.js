/* Who Broke It? — playable incident. Vanilla JS, no dependencies, no network calls.
   The incident (Maya, Dev, Sam, TASK-003, PaymentStatus, PR #184) is FICTIONAL and every output in it is SIMULATED
   in wbi's real output formats. The "Real CLI" section uses data.js, captured by scripts/site-data.py from real runs. */
(function () {
  'use strict';
  const $ = (s, r = document) => r.querySelector(s), $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const esc = s => String(s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const sleep = ms => new Promise(r => setTimeout(r, reduce ? 0 : ms));
  const scrollTo = (el, block) => el.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: block || 'start' });

  /* ---------- signals (a step can wait for a button) ---------- */
  const sig = {};
  const dfd = () => { let r; const p = new Promise(x => (r = x)); return { p, r }; };
  const waitSig = n => (sig[n] ??= dfd()).p;
  const fire = n => (sig[n] ??= dfd()).r();

  /* ---------- tracker ---------- */
  const learned = new Set();
  function learn(c) {
    if (!c) return;
    learned.add(c);
    const chip = $(`#chips li[data-c="${c}"]`);
    if (chip) chip.classList.add('on');
    $('#trkcount').textContent = $$('#chips li.on').length + '/' + $$('#chips li').length;
  }

  /* ---------- terminals ---------- */
  function prepTerm(term) {
    const out = $('.out', term);
    if (!out || term.dataset.static) return;
    const lines = out.innerHTML.replace(/^\n/, '').split('\n');
    out.innerHTML = lines.map(l => '<span class="ln">' + (l || ' ') + '</span>').join('');
    const c = $('.cmd', term);
    term._cmd = c.textContent.replace(/^\$ /, '');
    c.textContent = '';
    term.addEventListener('click', () => { term._skip = true; });
  }
  async function typeCmd(term) {
    const c = $('.cmd', term), full = '$ ' + term._cmd;
    c.innerHTML = '<span class="t"></span><span class="caret"> </span>';
    const t = $('.t', c), per = Math.max(6, Math.min(26, 1300 / full.length));
    for (let i = 1; i <= full.length && !term._skip; i++) { t.textContent = full.slice(0, i); await sleep(per); }
    t.textContent = full;
    $('.caret', c).remove();
  }
  async function playTerm(term) {
    if (term.dataset.wait) await waitSig(term.dataset.wait);
    scrollTo(term, 'nearest');
    await typeCmd(term);
    if (term.dataset.learn) learn(term.dataset.learn);
    const lns = $$('.ln', term), per = Math.max(6, Math.min(34, 1100 / lns.length));
    for (const l of lns) { l.classList.add('in'); if (!term._skip) await sleep(per); }
  }

  /* ---------- case flow ---------- */
  async function playCase(sec) {
    if (sec.id === 'standup') return playStandup(sec);
    if (sec.id === 'chaos') return;           // driven by its own button
    if (sec.id === 'later') setTimeout(() => $('.chain', sec) && $('.chain', sec).classList.add('go'), 600);
    for (const ch of [...sec.children]) {
      if (ch.matches('.term') && !ch.dataset.static) await playTerm(ch);
      else if (ch.matches('.quiz')) { ch.hidden = false; scrollTo(ch, 'nearest'); }
      else if (ch.matches('.after')) { ch.hidden = false; boards.forEach(b => b.redraw()); if (!ch.nextElementSibling || !ch.nextElementSibling.matches('.term')) scrollTo(ch, 'nearest'); }
    }
  }
  async function playStandup(sec) {
    for (const m of $$('.m', sec)) { m.classList.add('show'); await sleep(850); }
    await sleep(500);
    $$('.after', sec).forEach(a => (a.hidden = false));
  }
  function unlock(id, from) {
    const sec = document.getElementById(id);
    if (!sec || !sec.classList.contains('locked')) return;
    sec.classList.remove('locked');
    if (from) { from.disabled = true; from.setAttribute('aria-disabled', 'true'); }
    if (id === 'later') { const sm = document.getElementById('summary'); if (sm) sm.classList.remove('locked'); }
    boards.forEach(b => b.redraw());
    scrollTo(sec);
    if (id === 'judge') $('#prove').disabled = false;
    playCase(sec);
  }

  /* ---------- the detective board (SVG) ---------- */
  const N = {
    req:   { k: 'REQUIREMENT', t: 'REQ-002', s: 'Customers can pay', d: { what: 'A product requirement: customers can pay and see their payment status.', owner: 'Maya (product)', depends: 'implemented by TASK-003, TASK-007, TASK-010', task: 'TASK-003 (backend layer)', files: 'n/a', pr: 'n/a' } },
    t001:  { k: 'TASK · DONE', t: 'TASK-001', s: 'Update checkout UI', st: 'DONE', d: { what: 'Checkout UI update. Finished; TASK-003 depends on it.', owner: 'Dev (human)', depends: 'nothing', task: 'unblocked TASK-003 and TASK-010', files: 'src/web/checkout/**', pr: 'merged earlier' } },
    t003:  { k: 'TASK · IN PROGRESS', t: 'TASK-003', s: 'Update PaymentStatus API', st: 'IN_PROGRESS', d: { what: 'Adds a "paused" state to PaymentStatus without breaking consumers. Provides the PaymentStatus contract.', owner: 'Sam', depends: 'TASK-001 (DONE)', task: 'executor claude@sam, branch wbi/TASK-003', files: 'allowed: src/api/payment.ts, payment.test.ts · restricted: src/auth/**, migrations/**', pr: 'PR #184 (simulated)' } },
    agent: { k: 'AGENT', t: 'claude@sam', s: 'Claude Code', d: { what: 'The agent session that holds TASK-003 (a label wbi records; agents declare their own name).', owner: 'Sam', depends: 'works in worktree .wbi/worktrees/TASK-003', task: 'TASK-003', files: 'src/api/payment.ts + 2 more (per git)', pr: 'commit a91f2c7' } },
    file:  { k: 'FILE', t: 'payment.ts', s: 'src/api/payment.ts', d: { what: 'The API file TASK-003 declared it would MODIFY.', owner: 'TASK-003 (allowed path)', depends: 'overlaps TASK-010 scope src/api/**', task: 'TASK-003', files: 'src/api/payment.ts', pr: 'PR #184' } },
    pr:    { k: 'COMMIT · PR', t: 'a91f2c7 · #184', s: 'wbi/TASK-003 → main', d: { what: 'The commit and pull request that carry the change. The PR body is built from the handoff.', owner: 'claude@sam, approved by Sam', depends: 'TASK-003', task: 'TASK-003', files: '2 files after the scope fix', pr: '#184 (simulated)' } },
    c:     { k: 'CONTRACT', t: 'PaymentStatus', s: 'v1 → v2 (type)', d: { what: 'The type every consumer reads: pending | paid | failed (+ paused in v2). Owned by exactly one task.', owner: 'TASK-003 (only its provider may change it)', depends: 'consumed by TASK-007, TASK-010, TASK-014', task: 'TASK-003', files: 'src/api/payment.ts', pr: 'PR #184' } },
    t007:  { k: 'TASK · IN PROGRESS', t: 'TASK-007', s: 'Billing dashboard', st: 'IN_PROGRESS', d: { what: 'Billing dashboard UI; reads PaymentStatus.', owner: 'Maya', depends: 'TASK-002 (but not TASK-003: a plan gap)', task: 'executor antigravity@maya', files: 'src/web/billing/**', pr: 'not opened yet' } },
    t010:  { k: 'TASK · IN PROGRESS', t: 'TASK-010', s: 'Payment reconciliation', st: 'IN_PROGRESS', d: { what: 'Reconciliation service; reads PaymentStatus.', owner: 'Dev', depends: 'TASK-001 (but not TASK-003: a plan gap)', task: 'executor codex@dev', files: 'src/api/** (overlaps TASK-003)', pr: 'not opened yet' } },
    t014:  { k: 'TASK · BLOCKED', t: 'TASK-014', s: 'Mobile checkout', st: 'BLOCKED', d: { what: 'Mobile checkout; waits for TASK-003.', owner: 'Maya', depends: 'TASK-003', task: 'unclaimed', files: 'mobile/**', pr: 'n/a' } },
    t016:  { k: 'TASK · BLOCKED', t: 'TASK-016', s: 'Ship mobile release', st: 'BLOCKED', d: { what: 'Release task; waits for TASK-014.', owner: 'Maya', depends: 'TASK-014', task: 'unclaimed', files: 'release/**', pr: 'n/a' } },
  };
  const E = [['t001', 't003', 'dependsOn'], ['req', 't003', 'implements'], ['t003', 'agent', 'executed by'], ['t003', 'file', 'modifies'], ['t003', 'pr', 'shipped in'], ['t003', 'c', 'provides'], ['c', 't007', 'consumed by'], ['c', 't010', 'consumed by'], ['c', 't014', 'consumed by'], ['t014', 't016', 'unblocks']];
  function layout(narrow) {
    if (narrow) return { w: 340, h: 342, nw: 104, nh: 46, pos: { t001: [4, 6], req: [232, 6], agent: [4, 70], t003: [118, 70], pr: [232, 70], file: [4, 138], c: [118, 138], t007: [4, 210], t010: [118, 210], t014: [232, 210], t016: [232, 282] } };
    return { w: 790, h: 372, nw: 160, nh: 54, pos: { t001: [8, 18], req: [8, 150], t003: [215, 70], agent: [215, 190], file: [215, 280], c: [430, 70], pr: [430, 190], t007: [622, 8], t010: [622, 98], t014: [622, 188], t016: [622, 290] } };
  }
  function clip(c, w, h, to) { // point on the rect border toward `to`
    const dx = to[0] - c[0], dy = to[1] - c[1];
    const k = Math.min(dx ? (w / 2) / Math.abs(dx) : 1e9, dy ? (h / 2) / Math.abs(dy) : 1e9, 1);
    return [c[0] + dx * k, c[1] + dy * k];
  }
  function drawBoard(svg, o) {
    const narrow = svg.parentElement.clientWidth < 600, L = layout(narrow), { nw, nh, pos } = L;
    svg.setAttribute('viewBox', `0 0 ${L.w} ${L.h}`);
    const dir = new Set(['t007', 't010', 't014']), ind = new Set(['t016']);
    const state = id => (!o.blast ? (o.sel === id ? 'sel' : '') : id === 'c' ? 'origin' : dir.has(id) ? 'direct' : ind.has(id) ? 'indirect' : 'dim');
    const ctr = id => [pos[id][0] + nw / 2, pos[id][1] + nh / 2];
    let h = '';
    for (const [a, b, lab] of E) {
      const ca = ctr(a), cb = ctr(b), p1 = clip(ca, nw, nh, cb), p2 = clip(cb, nw, nh, ca);
      let cls = 'edge';
      if (o.blast) cls += ((a === 'c' && dir.has(b)) || (a === 't014' && b === 't016')) ? ' hot' : ' dim';
      else if (o.sel) cls += (a === o.sel || b === o.sel) ? ' hot' : ' dim';
      h += `<path class="${cls}" d="M${p1[0]},${p1[1]} L${p2[0]},${p2[1]}"/>`;
      if (!narrow) h += `<text class="elabel" x="${(p1[0] + p2[0]) / 2}" y="${(p1[1] + p2[1]) / 2 - 3}" text-anchor="middle">${lab}</text>`;
    }
    const mx = Math.floor((nw - 18) / 6.4);
    const cut = s => (s.length > mx ? s.slice(0, mx - 1) + '…' : s);
    for (const id of Object.keys(N)) {
      const n = N[id], [x, y] = pos[id], st = state(id);
      const kind = id === 'c' ? 'contract' : id === 'agent' ? 'agent' : id === 'pr' ? 'pr' : id === 'file' ? 'file' : id === 'req' ? 'req' : 'task';
      let cls = `node kind-${kind} ${n.st ? 'st-' + n.st : ''} ${st}`;
      if (o.blast && st === 'dim') cls += ' dim';
      const tag = o.blast ? (st === 'origin' ? 'ORIGIN' : st === 'direct' ? 'DIRECT' : st === 'indirect' ? 'INDIRECT' : '') : '';
      const attrs = o.interactive ? ` tabindex="0" role="button" aria-label="${esc(n.t + ': ' + n.s)}"` : '';
      h += `<g class="${cls}" data-id="${id}" transform="translate(${x},${y})"${attrs}><title>${esc(n.t + ': ' + n.s)}</title>
        <rect class="card" width="${nw}" height="${nh}" rx="6"/><circle class="pin" cx="9" cy="8" r="3"/>
        <text class="k" x="18" y="12">${esc(narrow ? n.k.split(' ·')[0] : n.k)}</text>
        <text class="t" x="8" y="${narrow ? 28 : 29}">${esc(cut(n.t))}</text>${narrow ? '' : `<text class="s" x="8" y="44">${esc(cut(n.s))}</text>`}
        ${tag ? `<text class="tag2" x="${nw - 6}" y="${nh - 6}" text-anchor="end">${tag}</text>` : ''}</g>`;
    }
    svg.innerHTML = h;
  }
  const boards = [];
  function mountBoard(svgId, o) {
    const svg = document.getElementById(svgId); if (!svg) return;
    const b = { svg, o: Object.assign({ sel: null }, o) };
    const redraw = () => drawBoard(svg, b.o);
    b.redraw = redraw; boards.push(b); redraw();
    if (o.interactive) {
      const pick = g => {
        if (!g) return; b.o.sel = g.dataset.id === b.o.sel ? null : g.dataset.id; redraw();
        const det = $('#detail1'), n = N[b.o.sel];
        det.innerHTML = n ? `<h4>${esc(n.t)} <span class="dim">· ${esc(n.s)}</span></h4><dl><dt>What it is</dt><dd>${esc(n.d.what)}</dd><dt>Owner</dt><dd>${esc(n.d.owner)}</dd><dt>Depends on</dt><dd>${esc(n.d.depends)}</dd><dt>Related task</dt><dd>${esc(n.d.task)}</dd><dt>Files</dt><dd>${esc(n.d.files)}</dd><dt>PR / commit</dt><dd>${esc(n.d.pr)}</dd></dl>` : '<p class="dim">Pick a card.</p>';
        const again = $(`.node[data-id="${b.o.sel}"]`, svg); if (again) again.focus({ preventScroll: true });
      };
      svg.addEventListener('click', e => pick(e.target.closest('.node')));
      svg.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(e.target.closest('.node')); } });
    }
  }
  let rzT; addEventListener('resize', () => { clearTimeout(rzT); rzT = setTimeout(() => boards.forEach(b => b.redraw()), 150); });

  /* ---------- quiz ---------- */
  const quiz = $('#quiz');
  if (quiz) quiz.addEventListener('click', e => {
    const b = e.target.closest('.qopt'); if (!b || b.disabled) return;
    const fb = $('#qfb');
    if (b.dataset.ok === '1') {
      b.classList.add('good'); $$('.qopt', quiz).forEach(x => (x.disabled = true));
      fb.style.color = 'var(--green)'; fb.textContent = '✓ Right. Claude never declared src/auth/**, and TASK-003 is not allowed to touch it. Run the merge gate on the branch:';
      fire('quizdone');
    } else {
      b.classList.add('bad'); setTimeout(() => b.classList.remove('bad'), 500);
      fb.style.color = 'var(--warn)'; fb.textContent = 'Not that one: it appears in the declared intents above. Look for the file with no matching line.';
    }
  });

  /* ---------- judge ---------- */
  const prove = $('#prove'); if (prove) prove.addEventListener('click', () => { prove.disabled = true; fire('prove'); });
  const refix = $('#refix'); if (refix) refix.addEventListener('click', () => { refix.disabled = true; fire('refix'); });

  /* ---------- chaos mode (scripted, SIMULATED) ---------- */
  const arena = $('#arena');
  const A = k => $(`.agent[data-a="${k}"]`), F = k => $(`.file[data-f="${k}"]`);
  const feedEl = $('#feed');
  function feed(text, cls) { const s = document.createElement('span'); s.className = 'ln in ' + (cls || ''); s.textContent = text; feedEl.appendChild(s); feedEl.scrollTop = feedEl.scrollHeight; }
  function setSt(k, text, cls) { const a = A(k); $('[data-st]', a).textContent = text; a.classList.remove('ok', 'bad', 'warn'); if (cls) a.classList.add(cls); }
  function flash(f, cls) { const el = F(f); el.classList.remove('flash-ok', 'flash-bad', 'flash-warn'); void el.offsetWidth; el.classList.add('flash-' + cls); }
  async function fly(from, to, label, cls, bounce) {
    if (reduce) return;
    const ar = arena.getBoundingClientRect(), a = from.getBoundingClientRect(), b = to.getBoundingClientRect();
    const p = document.createElement('span'); p.className = 'pkt ' + (cls || ''); p.textContent = label; arena.appendChild(p);
    const pt = r => [r.left - ar.left + r.width / 2, r.top - ar.top + r.height / 2];
    const s = pt(a), t = pt(b), mid = [(s[0] + t[0]) / 2, (s[1] + t[1]) / 2 - 22];
    const kf = [{ transform: `translate(${s[0] - 30}px,${s[1] - 10}px)` }, { transform: `translate(${mid[0] - 30}px,${mid[1]}px)` }, { transform: `translate(${t[0] - 30}px,${t[1] - 10}px)` }];
    if (bounce) kf.push({ transform: `translate(${s[0] - 30}px,${s[1] - 10}px)`, opacity: .2 });
    const dur = bounce ? 1500 : 1000;
    const anim = p.animate(kf, { duration: dur, easing: 'ease-in-out', fill: 'forwards' });
    // bounded: a throttled or background tab must never leave the replay hanging on animation.finished
    await Promise.race([anim.finished.catch(() => {}), new Promise(r => setTimeout(r, dur + 250))]);
    p.remove();
  }
  let caught = 0, clean = 0, running = false;
  const score = () => { $('#caught').textContent = caught; $('#clean').textContent = clean; };
  async function chaos() {
    if (running) return; running = true;
    const boom = $('#boom'); boom.disabled = true; $('#score').hidden = false; caught = clean = 0; score();
    $('#ver').textContent = 'PaymentStatus v1'; ['agy', 'claude', 'codex', 'human'].forEach(k => setSt(k, 'idle'));
    feedEl.innerHTML = '';
    feed('SIMULATION: scripted replay, no agent is running. Codes below are wbi\'s real conflict codes.', 'l-dim');
    feed('$ (4 workers) wbi start <task> --worktree', 'l-hd');
    setSt('agy', 'wbi start TASK-007 --worktree'); setSt('claude', 'wbi start TASK-003 --worktree'); setSt('codex', 'wbi start TASK-010 --worktree'); setSt('human', 'wbi start TASK-001 --worktree');
    await sleep(1100);

    feed('$ wbi intent declare CREATE src/web/billing/** --task TASK-007   # antigravity', 'l-hd');
    feed('✓ intent #6 registered: CREATE src/web/billing/**', 'l-ok'); setSt('agy', 'intent registered, editing…');
    await fly(A('agy'), F('billing'), 'commit 7c1e0aa'); flash('billing', 'ok'); setSt('agy', '✓ landed clean', 'ok'); clean++; score();
    await sleep(500);

    feed('$ wbi intent declare MODIFY src/api/payment.ts --task TASK-003   # claude', 'l-hd');
    feed('✓ intent #7 registered: MODIFY src/api/payment.ts', 'l-ok'); setSt('claude', 'intent registered, editing…');
    await fly(A('claude'), F('payment'), 'commit a91f2c7'); flash('payment', 'ok'); setSt('claude', '✓ landed clean', 'ok'); clean++; score();
    await sleep(500);

    feed('$ wbi intent declare MODIFY src/api/payment.ts --task TASK-010   # codex', 'l-hd');
    feed('  ⚠ WARN  OVERLAP claude@sam (TASK-003) declared MODIFY src/api/payment.ts, which overlaps your MODIFY', 'l-warn');
    feed('$ wbi intent declare CHANGE_CONTRACT PaymentStatus --task TASK-010', 'l-hd');
    feed('  ✖ BLOCK CONTRACT_NOT_OWNED PaymentStatus is owned by TASK-003. Only its provider may change it.', 'l-bad');
    feed('✖ Intent NOT registered. Resolve the blocking conflict(s) first.', 'l-bad');
    setSt('codex', 'trying to edit PaymentStatus…');
    await fly(A('codex'), F('payment'), 'edit contract', 'red', true); flash('payment', 'bad'); setSt('codex', '✖ BLOCKED before editing', 'bad'); caught++; score();
    await sleep(600);

    feed('$ wbi intent declare MODIFY src/auth/payment-token.ts --task TASK-001   # human, "quick fix"', 'l-hd');
    feed('  ⚠ WARN  OUT_OF_SCOPE src/auth/payment-token.ts is outside TASK-001\'s allowed paths (src/web/checkout/**).', 'l-warn');
    setSt('human', 'editing an auth file…');
    await fly(A('human'), F('auth'), 'commit 3be90d1', 'amber'); flash('auth', 'warn');
    feed('$ wbi check --base main --head wbi/TASK-001', 'l-hd');
    feed('✗ SCOPE  src/auth/payment-token.ts is outside TASK-001\'s allowed paths   → FAIL', 'l-bad');
    setSt('human', '⚠ flagged: out of scope', 'warn'); caught++; score();
    await sleep(600);

    feed('$ wbi handoff TASK-003 --contract "PaymentStatus=adds paused"   # claude', 'l-hd');
    feed('Contract: PaymentStatus → v2: adds paused', 'l-ok');
    $('#ver').textContent = 'PaymentStatus v2'; flash('payment', 'warn');
    feed('→ inbox TASK-007: PaymentStatus changed to v2 by TASK-003. This task consumes PaymentStatus.', 'l-warn');
    feed('→ inbox TASK-010: PaymentStatus changed to v2 by TASK-003. This task consumes PaymentStatus.', 'l-warn');
    feed('→ inbox TASK-014: PaymentStatus changed to v2 by TASK-003. This task consumes PaymentStatus.', 'l-warn');
    setSt('agy', '✓ landed clean · ⚠ PaymentStatus v2: adapt, then wbi ack', 'warn'); setSt('codex', '✖ blocked earlier · ⚠ PaymentStatus v2 notice', 'bad'); caught++; score();
    await sleep(700);
    feed('— end of simulation: caught before merge ' + caught + ', landed clean ' + clean + '. (A website demo.)', 'l-dim');
    $('#chaosafter').hidden = false; scrollTo($('#chaosafter'), 'nearest');
    boom.disabled = false; boom.textContent = '↺ Run it again'; running = false;
  }
  const boomBtn = $('#boom'); if (boomBtn) boomBtn.addEventListener('click', chaos);

  /* ---------- real CLI tabs (data.js) ---------- */
  const D = window.WBI_DEMO, T = (D && D.t) || {};
  function cls(line) {
    const t = line.trim();
    if (/^(✓|✔)/.test(t)) return 'l-ok';
    if (/^(✗|✖)/.test(t) || /\bBLOCK\b/.test(t) || /^FAIL\b/.test(t) || /Status: FAILED/.test(t)) return 'l-bad';
    if (/^(⚠|!)/.test(t) || /\bWARN\b/.test(t)) return 'l-warn';
    if (/^(WHO BROKE IT\?|BLAST RADIUS|EXECUTION PLAN|DRIFT REPORT|WAVE \d|WBI CHECK|PROJECT:|ATTENTION|ACTIVE|READY|TASK-\d+ (HANDOFF|VERIFICATION)|# TASK)/.test(t)) return 'l-hd';
    return '';
  }
  const REAL = [['simulate', 'plan', 'wbi simulate'], ['status', 'status', 'wbi status'], ['blast', 'blast', 'wbi blast BillingStatus'], ['blame', 'blame', 'wbi blame src/api/billing/'], ['why', 'why', 'wbi why src/api/billing/status.ts'], ['handoff + judge', 'judge', 'wbi handoff TASK-010 …'], ['check', 'gate', 'wbi check --base main --head wbi/TASK-010'], ['pr', 'pr', 'wbi pr TASK-007 --dry-run'], ['drift', 'drift', 'wbi drift'], ['sync', 'race', 'wbi sync   (two machines)']].filter(r => T[r[1]]);
  const rtabs = $('#rtabs'), rterm = $('#rterm');
  if (rtabs && REAL.length) {
    const show = i => {
      const [name, key, cmd] = REAL[i];
      let lines = T[key].replace(/\s+$/, '').split('\n'); if (/^\$ /.test(lines[0])) lines = lines.slice(1);
      rterm.className = 'term big';
      rterm.innerHTML = `<div class="bar"><i></i><i></i><i></i><span>REAL OUTPUT · $ ${esc(cmd)}</span></div><pre class="out">${lines.map(l => `<span class="${cls(l)}">${esc(l) || ' '}</span>`).join('\n')}</pre>`;
      $$('button', rtabs).forEach((b, j) => b.setAttribute('aria-selected', j === i));
    };
    rtabs.innerHTML = REAL.map((r, i) => `<button role="tab" aria-selected="${i === 0}" data-i="${i}">${esc(r[0])}</button>`).join('');
    rtabs.addEventListener('click', e => { const b = e.target.closest('button'); if (b) show(+b.dataset.i); });
    show(0);
  } else if (rterm) rterm.innerHTML = '<pre class="out">Transcripts failed to load. See docs/ in the repository.</pre>';

  /* ---------- install tabs + copy ---------- */
  $$('[data-copy]').forEach(box => {
    const b = $('button', box);
    b.addEventListener('click', async () => {
      const text = box.dataset.copy;
      try { await navigator.clipboard.writeText(text); } catch (e) { const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select(); try { document.execCommand('copy'); } catch (_) {} ta.remove(); }
      b.textContent = 'copied'; b.classList.add('ok'); setTimeout(() => { b.textContent = 'copy'; b.classList.remove('ok'); }, 1400);
    });
  });
  const itabs = $('#itabs');
  if (itabs) {
    $$('.itab').forEach((p, i) => (p.hidden = i !== 0));
    itabs.addEventListener('click', e => {
      const b = e.target.closest('button'); if (!b) return;
      $$('#itabs button').forEach(x => x.setAttribute('aria-selected', x === b));
      $$('.itab').forEach(p => (p.hidden = p.id !== 'i-' + b.dataset.i));
    });
  }

  /* ---------- init: progressive disclosure (without JS everything is simply visible) ---------- */
  mountBoard('board1', { interactive: true });
  mountBoard('board2', { blast: true });
  $$('.case .term').forEach(prepTerm);
  $$('.case .after, .case .quiz').forEach(el => (el.hidden = true));
  $$('.case .m').forEach(m => m.classList.remove('show'));
  const prv = $('#prove'); if (prv) prv.disabled = true;
  document.addEventListener('click', e => {
    const n = e.target.closest('.next[data-next]');
    if (n && !n.disabled) unlock(n.dataset.next, n);
  });
  $('#start').addEventListener('click', e => {
    $('#tracker').hidden = false; e.currentTarget.disabled = true; e.currentTarget.textContent = '🔎 Investigating…';
    unlock('standup');
  });
  const restart = $('#restart'); if (restart) restart.addEventListener('click', () => { location.hash = ''; location.reload(); });
})();
