"use client";

import { useState, useEffect, useCallback } from "react";

interface Note {
  id: number;
  title: string;
  content: string;
}

interface NoteEditorProps {
  note: Note;
  onSave: (id: number, title: string, content: string) => Promise<void>;
}

export default function NoteEditor({ note, onSave }: NoteEditorProps) {
  const [title, setTitle] = useState(note.title);
  const [content, setContent] = useState(note.content);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setTitle(note.title);
    setContent(note.content);
    setDirty(false);
  }, [note.id, note.title, note.content]);

  const handleSave = useCallback(async () => {
    if (!dirty) return;
    setSaving(true);
    try {
      await onSave(note.id, title, content);
      setDirty(false);
    } finally {
      setSaving(false);
    }
  }, [dirty, note.id, title, content, onSave]);

  // Render content with highlighted [[links]]
  const renderContentPreview = () => {
    const parts = content.split(/(\[\[[^\]]+\]\])/g);
    return parts.map((part, i) => {
      if (part.startsWith("[[") && part.endsWith("]]")) {
        return (
          <span key={i} className="text-obsidian-accent font-medium">
            {part}
          </span>
        );
      }
      return <span key={i}>{part}</span>;
    });
  };

  return (
    <div className="flex-1 flex flex-col h-screen bg-obsidian-editor">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-3 border-b border-obsidian-border">
        <div className="flex items-center gap-3 flex-1">
          <span className="text-obsidian-muted text-xs">
            {saving ? "Saving..." : dirty ? "Unsaved" : "Saved"}
          </span>
        </div>
        <button
          onClick={handleSave}
          disabled={!dirty || saving}
          className={`px-4 py-1 text-sm rounded transition-colors ${
            dirty
              ? "bg-obsidian-accent hover:bg-obsidian-accent-hover text-white"
              : "bg-obsidian-border text-obsidian-muted cursor-not-allowed"
          }`}
        >
          Save
        </button>
      </div>

      {/* Title */}
      <div className="px-6 pt-6">
        <input
          type="text"
          value={title}
          onChange={(e) => {
            setTitle(e.target.value);
            setDirty(true);
          }}
          placeholder="Note title"
          className="w-full text-3xl font-bold bg-transparent text-obsidian-text placeholder:text-obsidian-muted/50 border-none"
        />
      </div>

      {/* Help text */}
      <div className="px-6 pt-2 pb-1">
        <span className="text-xs text-obsidian-muted">
          Use <code className="text-obsidian-accent">[[Note Title]]</code> to link notes
        </span>
      </div>

      {/* Content */}
      <div className="flex-1 px-6 py-3 overflow-hidden">
        <textarea
          value={content}
          onChange={(e) => {
            setContent(e.target.value);
            setDirty(true);
          }}
          onBlur={handleSave}
          placeholder="Start writing..."
          className="w-full h-full bg-transparent text-obsidian-text placeholder:text-obsidian-muted/50 border-none resize-none font-mono text-sm leading-relaxed"
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === "s") {
              e.preventDefault();
              handleSave();
            }
          }}
        />
      </div>

      {/* Preview of links */}
      {content.includes("[[") && (
        <div className="px-6 py-3 border-t border-obsidian-border text-sm">
          <span className="text-obsidian-muted text-xs block mb-1">Preview:</span>
          <p className="text-obsidian-text/80 text-sm whitespace-pre-wrap line-clamp-3">
            {renderContentPreview()}
          </p>
        </div>
      )}
    </div>
  );
}
