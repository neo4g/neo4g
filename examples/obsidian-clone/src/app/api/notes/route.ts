import { NextResponse } from "next/server";
import { query, nodeToNote, type NeoNode } from "@/lib/neo4g";

export async function GET() {
  try {
    const result = await query('MATCH (n:Note) RETURN n');
    const notes = (result.rows || []).map((row) => nodeToNote(row.n as NeoNode));
    notes.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
    return NextResponse.json(notes);
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}

export async function POST(request: Request) {
  try {
    const { title } = await request.json();
    const now = new Date().toISOString();
    const result = await query(
      `CREATE (n:Note {title: "${title || "Untitled"}", content: "", createdAt: "${now}"})`
    );
    const created = result.rows[0]?.created as Record<string, unknown> | undefined;
    if (created) {
      return NextResponse.json({
        id: created.id as number,
        title: (created as Record<string, unknown>).properties
          ? ((created as Record<string, unknown>).properties as Record<string, unknown>).title
          : title || "Untitled",
        content: "",
        createdAt: now,
      });
    }
    // Fetch the latest note by scanning all
    const all = await query('MATCH (n:Note) RETURN n');
    const notes = (all.rows || []).map((row) => nodeToNote(row.n as NeoNode));
    notes.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
    return NextResponse.json(notes[0]);
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}
