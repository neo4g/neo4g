"use client";

import Link from "next/link";

interface Note {
  id: number;
  title: string;
}

interface SidebarProps {
  notes: Note[];
  selectedId: number | null;
  searchQuery: string;
  onSearchChange: (q: string) => void;
  onSelect: (id: number) => void;
  onNew: () => void;
  onDelete: (id: number) => void;
}

export default function Sidebar({
  notes,
  selectedId,
  searchQuery,
  onSearchChange,
  onSelect,
  onNew,
  onDelete,
}: SidebarProps) {
  const filtered = notes.filter((n) =>
    n.title.toLowerCase().includes(searchQuery.toLowerCase())
  );

  return (
    <aside className="w-64 h-screen flex flex-col bg-obsidian-sidebar border-r border-obsidian-border shrink-0">
      {/* Logo / Home */}
      <Link
        href="/"
        className="px-3 py-3 border-b border-obsidian-border flex items-center gap-2 hover:bg-obsidian-editor transition-colors"
      >
        <svg
          width="18"
          height="18"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className="text-obsidian-accent"
        >
          <circle cx="12" cy="12" r="3" />
          <circle cx="5" cy="6" r="2" />
          <circle cx="19" cy="6" r="2" />
          <circle cx="5" cy="18" r="2" />
          <circle cx="19" cy="18" r="2" />
          <line x1="9.5" y1="10.5" x2="6.5" y2="7.5" />
          <line x1="14.5" y1="10.5" x2="17.5" y2="7.5" />
          <line x1="9.5" y1="13.5" x2="6.5" y2="16.5" />
          <line x1="14.5" y1="13.5" x2="17.5" y2="16.5" />
        </svg>
        <span className="text-sm font-semibold text-obsidian-text">
          Neo4g Notes
        </span>
      </Link>

      {/* Search */}
      <div className="p-3 border-b border-obsidian-border">
        <input
          type="text"
          placeholder="Search notes..."
          value={searchQuery}
          onChange={(e) => onSearchChange(e.target.value)}
          className="w-full px-3 py-1.5 text-sm bg-obsidian-input border border-obsidian-border rounded text-obsidian-text placeholder:text-obsidian-muted"
        />
      </div>

      {/* Note list */}
      <div className="flex-1 overflow-y-auto">
        {filtered.length === 0 && (
          <p className="p-3 text-sm text-obsidian-muted">No notes found</p>
        )}
        {filtered.map((note) => (
          <div
            key={note.id}
            onClick={() => onSelect(note.id)}
            className={`group flex items-center justify-between px-3 py-2 cursor-pointer border-b border-obsidian-border/50 hover:bg-obsidian-editor transition-colors ${
              selectedId === note.id
                ? "bg-obsidian-editor border-l-2 border-l-obsidian-accent"
                : ""
            }`}
          >
            <span className="text-sm truncate flex-1 pr-2">
              {note.title || "Untitled"}
            </span>
            <button
              onClick={(e) => {
                e.stopPropagation();
                onDelete(note.id);
              }}
              className="opacity-0 group-hover:opacity-100 text-obsidian-muted hover:text-red-400 text-xs transition-opacity"
              title="Delete note"
            >
              ✕
            </button>
          </div>
        ))}
      </div>

      {/* New note button */}
      <div className="p-3 border-t border-obsidian-border">
        <button
          onClick={onNew}
          className="w-full px-3 py-1.5 text-sm bg-obsidian-accent hover:bg-obsidian-accent-hover text-white rounded transition-colors"
        >
          + New Note
        </button>
      </div>
    </aside>
  );
}
