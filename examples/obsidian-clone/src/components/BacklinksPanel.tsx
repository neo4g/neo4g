"use client";

import Link from "next/link";

interface LinkNote {
  id: number;
  title: string;
}

interface BacklinksPanelProps {
  incoming: LinkNote[];
  outgoing: LinkNote[];
  onSelect: (id: number) => void;
}

export default function BacklinksPanel({
  incoming,
  outgoing,
  onSelect,
}: BacklinksPanelProps) {
  return (
    <aside className="w-64 h-screen flex flex-col bg-obsidian-sidebar border-l border-obsidian-border overflow-y-auto shrink-0">
      {/* Graph link */}
      <Link
        href="/"
        className="px-3 py-2 border-b border-obsidian-border flex items-center gap-2 text-xs text-obsidian-muted hover:text-obsidian-accent transition-colors"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        >
          <circle cx="12" cy="12" r="3" />
          <circle cx="5" cy="6" r="2" />
          <circle cx="19" cy="6" r="2" />
          <line x1="9.5" y1="10.5" x2="6.5" y2="7.5" />
          <line x1="14.5" y1="10.5" x2="17.5" y2="7.5" />
        </svg>
        Open Graph View
      </Link>

      {/* Backlinks (incoming) */}
      <div className="p-3 border-b border-obsidian-border">
        <h3 className="text-xs font-semibold text-obsidian-muted uppercase tracking-wider mb-2">
          Backlinks ({incoming.length})
        </h3>
        {incoming.length === 0 ? (
          <p className="text-xs text-obsidian-muted/60">
            No other notes link here
          </p>
        ) : (
          <ul className="space-y-1">
            {incoming.map((note) => (
              <li key={note.id}>
                <button
                  onClick={() => onSelect(note.id)}
                  className="w-full text-left px-2 py-1 text-sm text-obsidian-accent hover:bg-obsidian-editor rounded transition-colors truncate"
                >
                  {note.title}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Outgoing links */}
      <div className="p-3">
        <h3 className="text-xs font-semibold text-obsidian-muted uppercase tracking-wider mb-2">
          Links to ({outgoing.length})
        </h3>
        {outgoing.length === 0 ? (
          <p className="text-xs text-obsidian-muted/60">
            Use [[Note Title]] to create links
          </p>
        ) : (
          <ul className="space-y-1">
            {outgoing.map((note) => (
              <li key={note.id}>
                <button
                  onClick={() => onSelect(note.id)}
                  className="w-full text-left px-2 py-1 text-sm text-obsidian-accent hover:bg-obsidian-editor rounded transition-colors truncate"
                >
                  {note.title}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}
