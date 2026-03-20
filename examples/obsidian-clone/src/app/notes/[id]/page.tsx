"use client";

import { useState, useEffect, useCallback } from "react";
import { useParams, useRouter } from "next/navigation";
import Sidebar from "@/components/Sidebar";
import NoteEditor from "@/components/NoteEditor";
import BacklinksPanel from "@/components/BacklinksPanel";

interface Note {
  id: number;
  title: string;
  content: string;
  createdAt: string;
}

interface LinkNote {
  id: number;
  title: string;
}

export default function NotePage() {
  const params = useParams();
  const router = useRouter();
  const noteId = parseInt(params.id as string, 10);

  const [notes, setNotes] = useState<Note[]>([]);
  const [currentNote, setCurrentNote] = useState<Note | null>(null);
  const [links, setLinks] = useState<{
    incoming: LinkNote[];
    outgoing: LinkNote[];
  }>({ incoming: [], outgoing: [] });
  const [searchQuery, setSearchQuery] = useState("");

  const fetchNotes = useCallback(async () => {
    try {
      const res = await fetch("/api/notes");
      if (!res.ok) throw new Error("Failed to fetch notes");
      setNotes(await res.json());
    } catch (err) {
      console.error(err);
    }
  }, []);

  const fetchNote = useCallback(async (id: number) => {
    try {
      const res = await fetch(`/api/notes/${id}`);
      if (!res.ok) throw new Error("Failed to fetch note");
      setCurrentNote(await res.json());
    } catch (err) {
      console.error(err);
    }
  }, []);

  const fetchLinks = useCallback(async (id: number) => {
    try {
      const res = await fetch(`/api/notes/${id}/links`);
      if (!res.ok) throw new Error("Failed to fetch links");
      setLinks(await res.json());
    } catch {
      setLinks({ incoming: [], outgoing: [] });
    }
  }, []);

  useEffect(() => {
    fetchNotes();
  }, [fetchNotes]);

  useEffect(() => {
    if (noteId) {
      fetchNote(noteId);
      fetchLinks(noteId);
    }
  }, [noteId, fetchNote, fetchLinks]);

  const handleSelect = useCallback(
    (id: number) => {
      router.push(`/notes/${id}`);
    },
    [router]
  );

  const handleNew = useCallback(async () => {
    try {
      const res = await fetch("/api/notes", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: "Untitled" }),
      });
      if (!res.ok) throw new Error("Failed to create note");
      const note = await res.json();
      await fetchNotes();
      router.push(`/notes/${note.id}`);
    } catch (err) {
      console.error(err);
    }
  }, [fetchNotes, router]);

  const handleDelete = useCallback(
    async (id: number) => {
      try {
        await fetch(`/api/notes/${id}`, { method: "DELETE" });
        await fetchNotes();
        if (id === noteId) {
          router.push("/");
        }
      } catch (err) {
        console.error(err);
      }
    },
    [noteId, fetchNotes, router]
  );

  const handleSave = useCallback(
    async (id: number, title: string, content: string) => {
      try {
        const res = await fetch(`/api/notes/${id}`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ title, content }),
        });
        if (!res.ok) throw new Error("Failed to save note");
        const updated = await res.json();
        setCurrentNote(updated);
        await fetchNotes();
        await fetchLinks(id);
      } catch (err) {
        console.error(err);
      }
    },
    [fetchNotes, fetchLinks]
  );

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        notes={notes}
        selectedId={noteId}
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        onSelect={handleSelect}
        onNew={handleNew}
        onDelete={handleDelete}
      />

      {currentNote ? (
        <NoteEditor note={currentNote} onSave={handleSave} />
      ) : (
        <div className="flex-1 flex items-center justify-center bg-obsidian-editor">
          <p className="text-obsidian-muted">Loading...</p>
        </div>
      )}

      <BacklinksPanel
        incoming={links.incoming}
        outgoing={links.outgoing}
        onSelect={handleSelect}
      />
    </div>
  );
}
