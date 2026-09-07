const paths: Record<string, string> = {
  spinner: '<path d="M12 3a9 9 0 1 0 9 9"/>',
  projects: '<path d="M3 7h6l2 2h10v10H3z"/><path d="M3 7V5h6l2 2"/>',
  tasks: '<path d="m3 5 2 2 4-4"/><path d="M11 6h10"/><path d="m3 12 2 2 4-4"/><path d="M11 13h10"/><path d="m3 19 2 2 4-4"/><path d="M11 20h10"/>',
  knowledge: '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>',
  model: '<rect width="18" height="18" x="3" y="3" rx="2"/><rect width="8" height="8" x="8" y="8" rx="1"/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  send: '<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>',
  attachment: '<path d="m21.4 11.6-8.9 8.9a6 6 0 0 1-8.5-8.5l9.6-9.6a4 4 0 0 1 5.7 5.7l-9.6 9.6a2 2 0 0 1-2.8-2.8l8.9-8.9"/>',
  server: '<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01M6 18h.01"/>',
  monitor: '<rect width="20" height="14" x="2" y="3" rx="2"/><path d="M8 21h8M12 17v4"/>',
  bot: '<rect width="18" height="12" x="3" y="8" rx="2"/><path d="M12 4v4M8 13h.01M16 13h.01"/>',
  branch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  refresh: '<path d="M20 6v6h-6"/><path d="M4 18v-6h6"/><path d="M6.5 9a7 7 0 0 1 11.8-2.6L20 8M4 16l1.7 1.6A7 7 0 0 0 17.5 15"/>',
  edit: '<path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/>',
  logout: '<path d="M10 17l5-5-5-5M15 12H3"/><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/>',
  menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
  close: '<path d="M18 6 6 18M6 6l12 12"/>',
  shield: '<path d="M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3z"/><path d="m9 12 2 2 4-4"/>',
  copy: '<rect width="13" height="13" x="8" y="8" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>',
  save: '<path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2Z"/><path d="M17 21v-8H7v8M7 3v5h8"/>',
  filter: '<path d="M4 5h16l-6 7v5l-4 2v-7z"/>',
  context: '<path d="m12 2 9 5-9 5-9-5z"/><path d="m3 12 9 5 9-5"/><path d="m3 17 9 5 9-5"/>',
  hardware: '<rect x="5" y="5" width="14" height="14" rx="2"/><path d="M9 9h6v6H9zM9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3"/>',
  browser: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 6.5h.01M10 6.5h.01"/>',
  expand: '<path d="M8 3H3v5M16 3h5v5M8 21H3v-5M16 21h5v-5"/><path d="m3 8 6-6M21 8l-6-6M3 16l6 6M21 16l-6 6"/>',
  panel: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M14 4v16"/>',
  proxy: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18"/>',
  sync: '<path d="M20 7h-7a4 4 0 0 0-4 4v1"/><path d="m17 4 3 3-3 3"/><path d="M4 17h7a4 4 0 0 0 4-4v-1"/><path d="m7 20-3-3 3-3"/>',
};

export function icon(name: string, spin = false): string {
  const body = paths[name] || '<circle cx="12" cy="12" r="9"/>';
  const className = spin ? "icon spin" : "icon";
  return `<svg class="${className}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;
}
