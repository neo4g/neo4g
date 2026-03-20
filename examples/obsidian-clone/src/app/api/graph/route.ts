import { NextResponse } from "next/server";
import { query, type NeoNode } from "@/lib/neo4g";

export async function GET() {
  try {
    // Get all notes as graph nodes
    const nodesResult = await query("MATCH (n:Note) RETURN n");
    const nodes = nodesResult.rows.map((row) => {
      const n = row.n as NeoNode;
      return {
        id: n.id,
        title: (n.properties.title as string) || "Untitled",
      };
    });

    // Get all LINKS_TO edges
    const edges: { source: number; target: number }[] = [];
    try {
      const edgesResult = await query(
        "MATCH (a:Note)-[:LINKS_TO]->(b:Note) RETURN a, b"
      );
      for (const row of edgesResult.rows) {
        const a = row.a as NeoNode;
        const b = row.b as NeoNode;
        edges.push({ source: a.id, target: b.id });
      }
    } catch {
      // No edges
    }

    return NextResponse.json({ nodes, edges });
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}
