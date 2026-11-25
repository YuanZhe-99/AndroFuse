// Shared interaction stubs for the AndroFuse V2.1 HTML drafts (no dependencies, file:// safe).
// They only demonstrate states named in README.md § "Design Vocabulary Audit"; the native
// MyGo views own the real behaviour.
(() => {
  // [Sticky Header] hairline + soft shadow only once content scrolls beneath the pinned header.
  for (const scroller of document.querySelectorAll('.pane-scroll')) {
    const head = scroller.querySelector('.ptop.sticky');
    if (!head) continue;
    const sync = () => head.classList.toggle('stuck', scroller.scrollTop > 2);
    scroller.addEventListener('scroll', sync, { passive: true });
    sync();
  }

  // [Switch vs. Checkbox] switches apply immediately; [Tabs vs. Segmented] segmented picks a value.
  document.addEventListener('click', (e) => {
    const sw = e.target.closest('.sw');
    if (sw && !sw.disabled) {
      sw.classList.toggle('on');
      sw.setAttribute('aria-checked', sw.classList.contains('on'));
    }
    const segBtn = e.target.closest('.seg button');
    if (segBtn && !segBtn.hasAttribute('onclick')) {
      for (const b of segBtn.parentElement.children) b.classList.toggle('active', b === segBtn);
    }
  });

  // [Loading Button] lock width, swap label for a centred spinner, siblings never move.
  for (const btn of document.querySelectorAll('[data-loading]')) {
    btn.addEventListener('click', (e) => {
      if (btn.classList.contains('loading')) return;
      e.preventDefault();
      btn.style.width = btn.getBoundingClientRect().width + 'px';
      btn.classList.add('loading');
      const ms = Number(btn.dataset.loading) || 1200;
      setTimeout(() => {
        btn.classList.remove('loading');
        btn.style.width = '';
        if (btn.href) location.href = btn.href;
      }, ms);
    });
  }

  // [Undo Toast] act immediately (row disappears) and offer Undo for 5 s.
  for (const trigger of document.querySelectorAll('[data-forget]')) {
    trigger.addEventListener('click', () => {
      const row = trigger.closest('.lr');
      const host = trigger.closest('.win, .mob') || document.body;
      row.hidden = true;
      const toast = document.createElement('div');
      toast.className = 'toast';
      toast.setAttribute('role', 'status');
      toast.innerHTML = `<span>已忘记「${trigger.dataset.forget}」</span><span class="bar"><i></i></span><button class="btn text" type="button">撤销</button>`;
      host.appendChild(toast);
      const done = setTimeout(() => toast.remove(), 5000);
      toast.querySelector('button').addEventListener('click', () => {
        clearTimeout(done);
        row.hidden = false;
        toast.remove();
      });
    });
  }

  // [Inline Validation] fields marked data-validate="digits6" / "port" validate as soon as it is clear.
  const rules = {
    digits6: (v) => /^\d{6}$/.test(v.replace(/\s/g, '')),
    port: (v) => /^\d+$/.test(v) && +v >= 1024 && +v <= 65535,
  };
  for (const input of document.querySelectorAll('[data-validate]')) {
    const field = input.closest('.field');
    const check = (final) => {
      const v = input.value.trim();
      if (!v) { field.classList.remove('invalid', 'valid'); return; }
      const ok = rules[input.dataset.validate](v);
      // Only flag an error once the value is complete (blur) or clearly too long.
      const clear = final || v.replace(/\s/g, '').length >= 6;
      field.classList.toggle('invalid', !ok && clear);
      field.classList.toggle('valid', ok);
    };
    input.addEventListener('input', () => check(false));
    input.addEventListener('blur', () => check(true));
    check(true);
  }

  // [Command Palette] Ctrl/⌘ K opens the palette board; Esc leaves it.
  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k' && document.querySelector('.win')) {
      e.preventDefault();
      location.href = 'DesktopCommandPalette.html';
    }
    if (e.key === 'Escape' && document.querySelector('.palette')) location.href = 'DesktopDevices.html';
  });
})();
