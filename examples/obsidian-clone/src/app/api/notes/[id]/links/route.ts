import { NextResponse } from "next/server";
import { query, nodeToNote, type NeoNode } from "@/lib/neo4g";

export async function GET(
  _request: Request,
  { params }: { params: { id: string } }
) {
  try {
    const id = parseInt(params.id, 10);

    let outgoing: { id: number; title: string }[] = [];
    let incoming: { id: number; title: string }[] = [];

    // Get outgoing links (notes this note links TO)
    try {
      const outResult = await query(
        `MATCH (n)-[:LINKS_TO]->(m) WHERE id(n) = ${id} RETURN m`
      );
      outgoing = outResult.rows.map((row) => {
        const note = nodeToNote(row.m as NeoNode);
        return { id: note.id, title: note.title };
      });
    } catch {
      // No outgoing links
    }

    // Get incoming links (notes that link TO this note — backlinks)
    try {
      const inResult = await query(
        `MATCH (m)-[:LINKS_TO]->(n) WHERE id(n) = ${id} RETURN m`
      );
      incoming = inResult.rows.map((row) => {
        const note = nodeToNote(row.m as NeoNode);
        return { id: note.id, title: note.title };
      });
    } catch {
      // No incoming links
    }

    return NextResponse.json({ outgoing, incoming });
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}
