// AndroFuse V3 HTML drafts: icon sprite, device avatars, theme switch and small state demos.
// file:// safe, no dependencies. The native MyGo views own the real behaviour.
(() => {
  const P = {
    home: '<path d="M4 11l8-6 8 6v8a1 1 0 0 1-1 1h-4v-6h-6v6H5a1 1 0 0 1-1-1z"/>',
    phone: '<rect x="7" y="3" width="10" height="18" rx="2"/><path d="M11 18h2"/>',
    dex: '<rect x="3" y="4" width="18" height="12" rx="1.5"/><path d="M8 20h8M12 16v4"/>',
    mirror: '<rect x="7" y="3" width="10" height="18" rx="2"/><path d="M10 8h4v6h-4z"/>',
    app: '<rect x="4" y="5" width="16" height="14" rx="2"/><path d="M4 9h16"/><circle cx="7" cy="7" r=".4"/>',
    apps: '<rect x="4" y="4" width="6" height="6" rx="1.5"/><rect x="14" y="4" width="6" height="6" rx="1.5"/><rect x="4" y="14" width="6" height="6" rx="1.5"/><rect x="14" y="14" width="6" height="6" rx="1.5"/>',
    camera: '<path d="M4 8h3l2-2h6l2 2h3v11H4z"/><circle cx="12" cy="13" r="3.2"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
    gear: '<circle cx="12" cy="12" r="3"/><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M5.6 18.4L7 17M17 7l1.4-1.4"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>',
    auto: '<rect x="3" y="4" width="18" height="13" rx="1.5"/><path d="M12 4v13" /><path d="M12 4h7.5a1.5 1.5 0 0 1 1.5 1.5v10a1.5 1.5 0 0 1-1.5 1.5H12z" fill="currentColor" stroke="none"/><path d="M8 21h8"/>',
    sidebar: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
    splitR: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M12 4v16"/>',
    splitD: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 12h18"/>',
    popout: '<path d="M14 4h6v6M20 4l-8 8"/><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>',
    popin: '<path d="M10 14H4v6M4 20l8-8"/><path d="M6 10V5a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1h-5"/>',
    max: '<rect x="5" y="5" width="14" height="14" rx="1"/>',
    close: '<path d="M6 6l12 12M18 6L6 18"/>',
    min: '<path d="M6 12h12"/>',
    more: '<circle cx="6" cy="12" r=".9" fill="currentColor"/><circle cx="12" cy="12" r=".9" fill="currentColor"/><circle cx="18" cy="12" r=".9" fill="currentColor"/>',
    back: '<path d="M15 6l-6 6 6 6"/>',
    circle: '<circle cx="12" cy="12" r="6"/>',
    recents: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
    rotate: '<path d="M4 12a8 8 0 0 1 14-5.3M20 12a8 8 0 0 1-14 5.3"/><path d="M18 3v4h-4M6 21v-4h4"/>',
    shot: '<path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3"/><circle cx="12" cy="12" r="3"/>',
    record: '<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="3.5" fill="currentColor"/>',
    clip: '<rect x="6" y="5" width="12" height="16" rx="2"/><path d="M9 5V4h6v1"/><path d="M9 11h6M9 15h4"/>',
    sound: '<path d="M4 10v4h4l5 4V6L8 10z"/><path d="M16 9a4 4 0 0 1 0 6M18.5 6.5a7.5 7.5 0 0 1 0 11"/>',
    screenoff: '<rect x="7" y="3" width="10" height="18" rx="2"/><path d="M14 9.5a3 3 0 1 1-3.5 3.8A2.5 2.5 0 0 0 14 9.5z"/>',
    full: '<path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/>',
    globe: '<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.5 2.5 3.5 5.5 3.5 8.5s-1 6-3.5 8.5c-2.5-2.5-3.5-5.5-3.5-8.5s1-6 3.5-8.5z"/>',
    usb: '<path d="M12 3v15"/><circle cx="12" cy="19.5" r="1.5"/><path d="M12 13l-4-2.5V8M12 11l4-2V6.5"/><rect x="6.8" y="6" width="2.4" height="2.2" rx=".4"/><circle cx="16" cy="5.5" r="1.1"/><path d="M10 5l2-2 2 2"/>',
    wifi: '<path d="M3 9.5a13 13 0 0 1 18 0M6 13a8.5 8.5 0 0 1 12 0M9 16.5a4 4 0 0 1 6 0"/><circle cx="12" cy="19.5" r=".8" fill="currentColor"/>',
    mesh: '<circle cx="6" cy="7" r="2"/><circle cx="18" cy="7" r="2"/><circle cx="12" cy="18" r="2"/><path d="M8 7h8M7 8.8l4 7.4M17 8.8l-4 7.4"/>',
    shield: '<path d="M12 3l7 3v5.5c0 4.3-3 7.7-7 9.5-4-1.8-7-5.2-7-9.5V6z"/>',
    info: '<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5M12 8v.2"/>',
    download: '<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',
    refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>',
    check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
    chevR: '<path d="M10 6l6 6-6 6"/>',
    chevD: '<path d="M6 10l6 6 6-6"/>',
    send: '<path d="M4 12l16-8-6 16-2.5-6.5z"/>',
    file: '<path d="M7 3h7l5 5v13H7z"/><path d="M14 3v5h5"/>',
    bell: '<path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2h-15z"/><path d="M10 20a2 2 0 0 0 4 0"/>',
    battery: '<rect x="3" y="8" width="16" height="9" rx="2"/><path d="M21 11v3"/><path d="M6 11h7v3H6z" fill="currentColor"/>',
    key: '<circle cx="8" cy="14" r="4"/><path d="M11 11l9-9M16 6l3 3M14 8l2 2"/>',
    eye: '<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/><circle cx="12" cy="12" r="3"/>',
    copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3"/>',
    trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
    edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13 7l4 4"/>',
    power: '<path d="M12 3v8"/><path d="M6.5 6.5a8 8 0 1 0 11 0"/>',
    stop: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
    folder: '<path d="M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"/>',
    log: '<path d="M6 3h9l4 4v14H6z"/><path d="M9 11h7M9 15h7M9 7h3"/>',
    pulse: '<path d="M3 12h4l2-5 4 10 2-5h6"/>',
    spark: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 16l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7z"/>',
    mic: '<rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21"/>',
    lock: '<rect x="5" y="10" width="14" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>',
    warn: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17v.2"/>',
    cpu: '<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3"/>',
    vm: '<rect x="2.5" y="4" width="14" height="10" rx="1.5"/><rect x="15" y="9" width="6.5" height="11" rx="1.5"/><path d="M6 18h6M9 14v4"/>',
    hand: '<path d="M8 13V6.5a1.5 1.5 0 0 1 3 0V12M11 11V5a1.5 1.5 0 0 1 3 0v6M14 11V6.5a1.5 1.5 0 0 1 3 0V14c0 4-2.5 7-6 7-2.5 0-4-1.2-5.5-3.5L3.8 15a1.5 1.5 0 0 1 2.5-1.6L8 15"/>',
    pin: '<path d="M12 21s-6-5.6-6-11a6 6 0 0 1 12 0c0 5.4-6 11-6 11z"/><circle cx="12" cy="10" r="2.2"/>',
    tray: '<path d="M4 14l2-9h12l2 9v5H4z"/><path d="M4 14h5l1 2h4l1-2h5"/>',
    link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
    update: '<path d="M12 3v10M8 9l4 4 4-4"/><path d="M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>',
  };
  const sprite = Object.entries(P).map(([k, v]) => `<symbol id="i-${k}" viewBox="0 0 24 24">${v}</symbol>`).join('');
  const logos = window.AF_LOGOS || {};
  const logoSyms = Object.entries(logos).map(([k, d]) => `<symbol id="logo-${k}" viewBox="0 0 24 24"><path d="${d}"/></symbol>`).join('');
  document.body.insertAdjacentHTML('afterbegin', `<svg width="0" height="0" style="position:absolute">${sprite}${logoSyms}</svg>`);

  // <i data-i="phone" class="s"> → inline icon
  for (const el of document.querySelectorAll('i[data-i]')) {
    el.outerHTML = `<svg class="i ${el.className}" aria-hidden="true"><use href="#i-${el.dataset.i}"/></svg>`;
  }

  // Device avatar (Kyokai deviceAvatar / deviceLogoName): the vendor mark when the maker, model, name or system
  // names a known maker (whole words only), else the first letter of the name shown.
  const makers = { samsung: ['samsung', 'galaxy'], google: ['google', 'pixel'], xiaomi: ['xiaomi', 'redmi', 'poco'], huawei: ['huawei'],
    sony: ['sony', 'xperia'], lg: ['lg'], lenovo: ['lenovo', 'motorola', 'moto'], asus: ['asus', 'rog', 'zenfone'] };
  const pick = (words) => {
    for (const w of words) {
      const toks = w.toLowerCase().split(/[^a-z0-9]+/);
      for (const [logo, keys] of Object.entries(makers)) if (keys.some((k) => toks.includes(k)) && logos[logo]) return logo;
    }
    return '';
  };
  for (const el of document.querySelectorAll('.av[data-dev]')) {
    if (el.dataset.glyph) { el.innerHTML = `<svg class="i" style="fill:none"><use href="#i-${el.dataset.glyph}"/></svg>`; continue; }
    const [maker = '', model = '', name = ''] = el.dataset.dev.split('|');
    const logo = pick([maker, model, name]);
    el.innerHTML = logo ? `<svg><use href="#logo-${logo}"/></svg>` : (name || model || maker).trim().charAt(0).toUpperCase();
  }

  // Theme: ?dark in the URL, or the toggle in the draft header.
  const root = document.documentElement;
  if (new URLSearchParams(location.search).has('dark')) root.classList.add('dark');
  for (const b of document.querySelectorAll('[data-theme-toggle]')) b.addEventListener('click', () => root.classList.toggle('dark'));

  document.addEventListener('click', (e) => {
    const sw = e.target.closest('.sw');
    if (sw) sw.classList.toggle('on');
    const chk = e.target.closest('.chk');
    if (chk) chk.classList.toggle('on');
    const seg = e.target.closest('.seg button');
    if (seg) for (const b of seg.parentElement.children) b.classList.toggle('on', b === seg);
    // Settings sheet: left sections switch the page.
    const nav = e.target.closest('[data-page]');
    if (nav) {
      for (const n of document.querySelectorAll('[data-page]')) n.classList.toggle('on', n === nav);
      for (const p of document.querySelectorAll('[data-spage]')) p.hidden = p.dataset.spage !== nav.dataset.page;
    }
  });
})();
