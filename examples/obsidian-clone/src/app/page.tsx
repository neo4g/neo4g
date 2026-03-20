"use client";

import { useState, useEffect, useCallback } from "react";
import { useRouter } from "next/navigation";
import Sidebar from "@/components/Sidebar";
import GraphView from "@/components/GraphView";

interface Note {
  id: number;
  title: string;
  content: string;
  createdAt: string;
}

interface GraphData {
  nodes: { id: number; title: string }[];
  edges: { source: number; target: number }[];
}

export default function Home() {
  const [notes, setNotes] = useState<Note[]>([]);
  const [graph, setGraph] = useState<GraphData>({ nodes: [], edges: [] });
  const [searchQuery, setSearchQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const router = useRouter();

  const fetchNotes = useCallback(async () => {
    try {
      const res = await fetch("/api/notes");
      if (!res.ok) throw new Error("Failed to fetch notes");
      const data = await res.json();
      setNotes(data);
      setError(null);
    } catch (err) {
      setError(
        "Cannot connect to Neo4g. Make sure the server is running on localhost:7474"
      );
      console.error(err);
    }
  }, []);

  const fetchGraph = useCallback(async () => {
    try {
      const res = await fetch("/api/graph");
      if (!res.ok) throw new Error("Failed to fetch graph");
      const data = await res.json();
      setGraph(data);
    } catch (err) {
      console.error(err);
    }
  }, []);

  useEffect(() => {
    fetchNotes();
    fetchGraph();
  }, [fetchNotes, fetchGraph]);

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
      router.push(`/notes/${note.id}`);
    } catch (err) {
      console.error(err);
    }
  }, [router]);

  const handleDelete = useCallback(
    async (id: number) => {
      try {
        await fetch(`/api/notes/${id}`, { method: "DELETE" });
        await Promise.all([fetchNotes(), fetchGraph()]);
      } catch (err) {
        console.error(err);
      }
    },
    [fetchNotes, fetchGraph]
  );

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        notes={notes}
        selectedId={null}
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        onSelect={handleSelect}
        onNew={handleNew}
        onDelete={handleDelete}
      />

      {error ? (
        <div className="flex-1 flex items-center justify-center bg-obsidian-bg">
          <div className="text-center p-8 max-w-md">
            <div className="text-4xl mb-4">&#x26A0;</div>
            <h2 className="text-xl font-bold text-obsidian-text mb-2">
              Connection Error
            </h2>
            <p className="text-obsidian-muted text-sm mb-4">{error}</p>
            <button
              onClick={() => {
                fetchNotes();
                fetchGraph();
              }}
              className="px-4 py-2 bg-obsidian-accent hover:bg-obsidian-accent-hover text-white rounded text-sm transition-colors"
            >
              Retry
            </button>
          </div>
        </div>
      ) : graph.nodes.length === 0 ? (
        <div className="flex-1 flex items-center justify-center bg-obsidian-bg">
          <div className="text-center">
            <h2 className="text-2xl font-bold text-obsidian-muted/40 mb-2">
              Graph View
            </h2>
            <p className="text-obsidian-muted/40 text-sm mb-4">
              Create notes and link them with [[Note Title]] to see the graph
            </p>
            <button
              onClick={handleNew}
              className="px-4 py-2 bg-obsidian-accent hover:bg-obsidian-accent-hover text-white rounded text-sm transition-colors"
            >
              Create First Note
            </button>
          </div>
        </div>
      ) : (
        <div className="flex-1 flex flex-col bg-obsidian-bg">
          <div className="px-4 py-2 border-b border-obsidian-border flex items-center justify-between">
            <h2 className="text-sm font-medium text-obsidian-muted">
              Graph View
            </h2>
            <span className="text-xs text-obsidian-muted/60">
              {graph.nodes.length} notes &middot; {graph.edges.length} links
            </span>
          </div>
          <div className="flex-1">
            <GraphView nodes={graph.nodes} edges={graph.edges} />
          </div>
        </div>
      )}
    </div>
  );
}
