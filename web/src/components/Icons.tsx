// Minimal 1.5px line icons, drawn on a 24px grid, colored by currentColor.
import type { ReactNode } from "react";

function Svg({ children, size = 18 }: { children: ReactNode; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export const icons = {
  home: (
    <Svg>
      <path d="M4 11 12 4l8 7v9H4z" />
      <path d="M10 20v-5h4v5" />
    </Svg>
  ),
  files: (
    <Svg>
      <path d="M3 6h7l2 2h9v11H3z" />
    </Svg>
  ),
  storage: (
    <Svg>
      <rect x="3" y="4" width="18" height="7" rx="1" />
      <rect x="3" y="13" width="18" height="7" rx="1" />
      <path d="M7 7.5h.01M7 16.5h.01" />
    </Svg>
  ),
  upload: (
    <Svg>
      <path d="M12 16V4M7 9l5-5 5 5" />
      <path d="M4 16v4h16v-4" />
    </Svg>
  ),
  download: (
    <Svg>
      <path d="M12 4v12M7 11l5 5 5-5" />
      <path d="M4 16v4h16v-4" />
    </Svg>
  ),
  users: (
    <Svg>
      <circle cx="9" cy="8" r="3.5" />
      <path d="M2.5 20c.8-3.5 3.3-5.5 6.5-5.5s5.7 2 6.5 5.5" />
      <path d="M16 4.8a3.5 3.5 0 0 1 0 6.4M18.5 14.8c1.5.8 2.6 2.6 3 5.2" />
    </Svg>
  ),
  remote: (
    <Svg>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.7 3.8 5.7 3.8 9s-1.3 6.3-3.8 9c-2.5-2.7-3.8-5.7-3.8-9S9.5 5.7 12 3z" />
    </Svg>
  ),
  settings: (
    <Svg>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9 7 7M17 17l2.1 2.1M4.9 19.1 7 17M17 7l2.1-2.1" />
    </Svg>
  ),
  shield: (
    <Svg size={14}>
      <path d="M12 3 5 6v6c0 4.5 3 7.5 7 9 4-1.5 7-4.5 7-9V6z" />
    </Svg>
  ),
  refresh: (
    <Svg size={16}>
      <path d="M20 11a8 8 0 1 0-2.3 5.7M20 5v6h-6" />
    </Svg>
  ),
  keyboard: (
    <Svg size={16}>
      <rect x="2.5" y="6" width="19" height="12" rx="1.5" />
      <path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10" />
    </Svg>
  ),
};

export type IconName = keyof typeof icons;
