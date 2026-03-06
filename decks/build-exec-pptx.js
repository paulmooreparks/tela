const PptxGenJS = require('pptxgenjs');

const pptx = new PptxGenJS();
pptx.layout = 'LAYOUT_WIDE';
pptx.author = 'GitHub Copilot';
pptx.company = 'Parks Computing';
pptx.subject = 'Tela + Awan Saya executive overview';
pptx.title = 'Tela + Awan Saya — Secure Remote Access Without VPN';
pptx.lang = 'en-US';
pptx.theme = {
  headFontFace: 'Aptos Display',
  bodyFontFace: 'Aptos',
  lang: 'en-US'
};
pptx.defineSlideMaster({
  title: 'EXEC',
  background: { color: 'F7F8FA' },
  objects: [
    { rect: { x: 0, y: 0, w: 13.333, h: 0.22, fill: { color: '1F6F3F' }, line: { color: '1F6F3F' } } },
    { rect: { x: 0.4, y: 7.08, w: 12.5, h: 0.01, line: { color: 'D9DEE5', pt: 1 } } },
    { text: { text: 'Tela + Awan Saya', options: { x: 0.5, y: 7.12, w: 3.0, h: 0.2, fontFace: 'Aptos', fontSize: 9, color: '6B7280' } } },
    { text: { text: 'Executive overview', options: { x: 10.6, y: 7.12, w: 2.2, h: 0.2, align: 'right', fontFace: 'Aptos', fontSize: 9, color: '6B7280' } } }
  ],
  slideNumber: { x: 12.45, y: 7.12, w: 0.3, h: 0.2, color: '6B7280', fontFace: 'Aptos', fontSize: 9, align: 'right' }
});

const COLORS = {
  green: '1F6F3F',
  dark: '111827',
  muted: '4B5563',
  light: '6B7280',
  accent: 'E8F3EC',
  white: 'FFFFFF'
};

function addTitle(slide, title, subtitle) {
  slide.addText(title, {
    x: 0.65, y: 0.55, w: 12, h: 0.55,
    fontFace: 'Aptos Display', fontSize: 24, bold: true, color: COLORS.green,
    margin: 0
  });
  if (subtitle) {
    slide.addText(subtitle, {
      x: 0.65, y: 1.08, w: 12, h: 0.35,
      fontFace: 'Aptos', fontSize: 11, color: COLORS.light,
      margin: 0
    });
  }
}

function addBullets(slide, items, opts = {}) {
  const x = opts.x ?? 0.95;
  const y = opts.y ?? 1.6;
  const w = opts.w ?? 11.5;
  const h = opts.h ?? 4.8;
  const fontSize = opts.fontSize ?? 20;
  const indent = opts.indent ?? 18;
  const hanging = opts.hanging ?? 4;

  const runs = [];
  items.forEach((item, idx) => {
    if (typeof item === 'string') {
      runs.push({ text: item, options: { bullet: { indent }, hanging } });
    } else {
      runs.push(item);
    }
    if (idx !== items.length - 1) runs.push({ text: '\n' });
  });

  slide.addText(runs, {
    x, y, w, h,
    fontFace: 'Aptos', fontSize, color: COLORS.dark,
    breakLine: false,
    paraSpaceAfterPt: opts.paraSpaceAfterPt ?? 14,
    valign: 'top',
    margin: 0
  });
}

function addSectionLabel(slide, text, x, y, w = 3.2) {
  slide.addShape(pptx.ShapeType.roundRect, {
    x, y, w, h: 0.36,
    rectRadius: 0.05,
    line: { color: COLORS.accent, pt: 1 },
    fill: { color: COLORS.accent }
  });
  slide.addText(text, {
    x: x + 0.12, y: y + 0.05, w: w - 0.24, h: 0.2,
    fontFace: 'Aptos', fontSize: 10, bold: true, color: COLORS.green,
    margin: 0, align: 'left'
  });
}

function addTwoColBullets(slide, leftTitle, leftItems, rightTitle, rightItems) {
  addSectionLabel(slide, leftTitle, 0.75, 1.55, 2.4);
  addSectionLabel(slide, rightTitle, 6.75, 1.55, 2.4);
  addBullets(slide, leftItems, { x: 0.9, y: 2.05, w: 5.15, h: 4.4, fontSize: 18 });
  addBullets(slide, rightItems, { x: 6.9, y: 2.05, w: 5.15, h: 4.4, fontSize: 18 });
}

function addClosing(slide, text) {
  slide.addText(text, {
    x: 0.9, y: 6.2, w: 11.2, h: 0.45,
    fontFace: 'Aptos', fontSize: 16, bold: true, color: COLORS.green,
    margin: 0
  });
}

// Title slide
{
  const slide = pptx.addSlide('EXEC');
  slide.addShape(pptx.ShapeType.roundRect, {
    x: 0.65, y: 1.15, w: 4.4, h: 0.42,
    rectRadius: 0.06,
    line: { color: COLORS.accent, pt: 1 },
    fill: { color: COLORS.accent }
  });
  slide.addText('Executive overview for IT leadership', {
    x: 0.82, y: 1.23, w: 3.8, h: 0.18, fontSize: 11, color: COLORS.green, bold: true, margin: 0
  });
  slide.addText('Tela + Awan Saya', {
    x: 0.65, y: 1.9, w: 8.5, h: 0.75,
    fontFace: 'Aptos Display', fontSize: 28, bold: true, color: COLORS.green, margin: 0
  });
  slide.addText('Secure remote access without VPN friction', {
    x: 0.65, y: 2.72, w: 7.5, h: 0.45,
    fontFace: 'Aptos', fontSize: 18, color: COLORS.muted, margin: 0
  });
  slide.addShape(pptx.ShapeType.roundRect, {
    x: 0.65, y: 3.55, w: 5.0, h: 1.2,
    rectRadius: 0.08,
    line: { color: 'D4E6D8', pt: 1 },
    fill: { color: 'FFFFFF' },
    shadow: { type: 'outer', color: 'D1D5DB', angle: 45, blur: 1, distance: 1, opacity: 0.15 }
  });
  slide.addText([
    { text: 'Tela', options: { bold: true, color: COLORS.green } },
    { text: ' = connectivity fabric' },
    { text: '\n' },
    { text: 'Awan Saya', options: { bold: true, color: COLORS.green } },
    { text: ' = platform layer' }
  ], {
    x: 0.95, y: 3.87, w: 4.4, h: 0.6,
    fontFace: 'Aptos', fontSize: 18, color: COLORS.dark, margin: 0, valign: 'mid'
  });
}

// Executive summary
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Executive summary');
  addBullets(slide, [
    'IT teams need access to systems that are private, segmented, and locked down',
    'Traditional approaches often mean too much network access or too much operational overhead',
    'Tela provides narrow access to specific TCP services',
    'Awan Saya adds multi-hub visibility, discovery, access control, and onboarding on top'
  ], { y: 1.7, h: 3.8 });
  addClosing(slide, 'Bottom line: simpler remote access, smaller blast radius, less VPN friction.');
}

// Problem
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'The problem');
  addBullets(slide, [
    'Teams are distributed',
    'Infrastructure is behind NAT and firewalls',
    'Corporate endpoints often block admin installs, drivers, and TUN devices',
    'Security teams want least privilege, not flat network access'
  ], { y: 1.8, h: 3.8 });
  addClosing(slide, 'Result: remote access is slow to roll out and hard to control.');
}

// Existing approaches
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Why existing approaches fall short');
  addTwoColBullets(
    slide,
    'Network-first tools',
    [
      'VPNs / mesh VPNs — often require admin rights, drivers, or broad network trust',
      'Bastions / jump hosts — add infrastructure and create choke points'
    ],
    'Alternatives',
    [
      'HTTP tunnels — great for web apps, awkward for raw TCP services',
      'Large ZT platforms — powerful, but often heavy and expensive for small or mid-sized teams'
    ]
  );
}

// Tela in one slide
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Tela in one slide');
  slide.addText('Tela gives users secure access to TCP services without requiring a traditional VPN.', {
    x: 0.85, y: 1.55, w: 11.5, h: 0.4,
    fontFace: 'Aptos', fontSize: 20, bold: true, color: COLORS.dark, margin: 0
  });
  addBullets(slide, [
    'End-to-end encrypted userspace WireGuard tunnel',
    'Hub relays ciphertext only',
    'Client and agent are both outbound-only',
    'No admin privileges or TUN devices required',
    'Existing tools keep working through localhost'
  ], { y: 2.1, h: 3.4, fontSize: 18 });
  slide.addShape(pptx.ShapeType.roundRect, {
    x: 0.95, y: 5.85, w: 10.9, h: 0.65,
    rectRadius: 0.06,
    line: { color: 'D4E6D8', pt: 1 },
    fill: { color: COLORS.accent }
  });
  slide.addText('App → localhost → tela → hub → telad → target service', {
    x: 1.2, y: 6.03, w: 10.3, h: 0.22,
    align: 'center', fontFace: 'Aptos', fontSize: 18, bold: true, color: COLORS.green, margin: 0
  });
}

// Why Tela is different
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Why Tela is different');
  addBullets(slide, [
    'Zero-install, no-admin client',
    'Protocol-agnostic TCP tunneling',
    'Outbound-only connectivity',
    'End-to-end encryption through a blind relay',
    'Single-binary, lightweight deployment'
  ], { y: 1.85, h: 4.1 });
  addClosing(slide, 'This combination is what makes Tela practical in locked-down environments.');
}

// Where it fits best
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Where it fits best');
  addBullets(slide, [
    'Developer access to staging or production systems',
    'Bastion replacement for SSH, RDP, and database access',
    'MSP / IT support for customer environments behind NAT',
    'IoT and edge deployments in networks you do not control',
    'Training labs / classrooms without VPN rollout overhead'
  ], { y: 1.85, h: 4.5 });
}

// Operating model
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Operating model');
  addTwoColBullets(
    slide,
    'Two supported patterns',
    [
      'Endpoint agent — telad runs on each managed machine',
      'Gateway / bridge agent — telad runs on a gateway that can reach internal targets'
    ],
    'Control surface stays small',
    [
      'Expose only the specific service ports you want',
      'Avoid exposing an entire subnet or network segment'
    ]
  );
}

// Security view
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Security view');
  addBullets(slide, [
    'Encryption: end-to-end WireGuard tunnel',
    'Exposure: outbound-only from both sides',
    'Authentication: token-based today; rotate and manage as secrets',
    'Segmentation: one hub per environment, site, or customer is straightforward',
    'Auditability: hubs expose connection history and status APIs'
  ], { y: 1.85, h: 4.5 });
  addClosing(slide, 'For leadership: less network exposure, tighter scope, easier review.');
}

// Awan Saya role
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Awan Saya’s role');
  slide.addText('Tela is the engine. Awan Saya adds the platform features around it:', {
    x: 0.85, y: 1.55, w: 11.5, h: 0.35,
    fontFace: 'Aptos', fontSize: 18, color: COLORS.dark, margin: 0
  });
  addBullets(slide, [
    'Multi-hub dashboard',
    'Hub directory and name resolution',
    'Easier onboarding',
    'Shared view of machines, services, and sessions',
    'Foundation for centralized auth and RBAC'
  ], { y: 2.0, h: 3.8, fontSize: 18 });
  addClosing(slide, 'Analogy: Tela : Awan Saya :: git : GitHub');
}

// Platform changes for users
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'What the platform changes for users');
  addTwoColBullets(
    slide,
    'Without a platform layer',
    [
      'Users need to know specific hub URLs',
      'Onboarding is manual',
      'Multi-hub visibility is fragmented'
    ],
    'With Awan Saya',
    [
      'Users log in once',
      'Hubs can be discovered by name',
      'One dashboard shows fleet-wide status'
    ]
  );
}

// Adoption path
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Adoption path');
  addTwoColBullets(
    slide,
    'Start narrow',
    [
      'One hub',
      'One team',
      'Two or three services'
    ],
    'Prove value, then scale',
    [
      'Faster access setup',
      'Fewer inbound firewall changes',
      'Smaller blast radius than VPN',
      'One hub per environment, site, or customer',
      'Add Awan Saya for centralized visibility and discovery'
    ]
  );
}

// Recommended first pilot
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Recommended first pilot');
  addSectionLabel(slide, 'Choose one', 0.8, 1.6, 1.65);
  addBullets(slide, [
    'Developer access to staging',
    'Production bastion replacement',
    'MSP support into customer networks'
  ], { x: 0.95, y: 2.0, w: 5.2, h: 2.4, fontSize: 18 });
  addSectionLabel(slide, 'Measure success', 6.75, 1.6, 2.0);
  addBullets(slide, [
    'Time to onboard a user',
    'Number of inbound rules avoided',
    'Time to reach a target machine or service',
    'Auditability of who connected and when'
  ], { x: 6.9, y: 2.0, w: 5.3, h: 3.0, fontSize: 18 });
}

// Next steps
{
  const slide = pptx.addSlide('EXEC');
  addTitle(slide, 'Next steps');
  addBullets(slide, [
    'Stand up a hub',
    'Validate reachability and audit endpoints',
    'Connect one or two target services',
    'Onboard a small user group',
    'Add Awan Saya when multi-hub discovery and visibility become useful'
  ], { y: 1.9, h: 4.1 });
  addClosing(slide, 'Goal: move from ad hoc remote access to a repeatable connectivity fabric + platform model.');
}

pptx.writeFile({ fileName: 'decks/exec-tela-awansaya-editable.pptx' });
