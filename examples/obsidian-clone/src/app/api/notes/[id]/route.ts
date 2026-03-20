import { NextResponse } from "next/server";
import { query, nodeToNote, type NeoNode } from "@/lib/neo4g";

export async function GET(
  _request: Request,
  { params }: { params: { id: string } }
) {
  try {
    const id = parseInt(params.id, 10);
    const result = await query(
      `MATCH (n) WHERE id(n) = ${id} RETURN n`
    );
    if (result.rows.length === 0) {
      return NextResponse.json({ error: "Note not found" }, { status: 404 });
    }
    return NextResponse.json(nodeToNote(result.rows[0].n as NeoNode));
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}

export async function PUT(
  request: Request,
  { params }: { params: { id: string } }
) {
  try {
    const id = parseInt(params.id, 10);
    const { title, content } = await request.json();

    // Escape double quotes in strings for Cypher
    const safeTitle = (title as string).replace(/"/g, '\\"');
    const safeContent = (content as string).replace(/"/g, '\\"');

    await query(
      `MATCH (n) WHERE id(n) = ${id} SET n.title = "${safeTitle}", n.content = "${safeContent}"`
    );

    // Sync wiki-links: delete existing outgoing LINKS_TO, then create new ones
    // Delete outgoing links
    try {
      const existingLinks = await query(
        `MATCH (n)-[e:LINKS_TO]->(m) WHERE id(n) = ${id} RETURN e, m`
      );
      for (const row of existingLinks.rows) {
        const target = row.m as NeoNode;
        await query(
          `MATCH (a)-[e:LINKS_TO]->(b) WHERE id(a) = ${id} AND id(b) = ${target.id} DELETE e`
        );
      }
    } catch {
      // No existing links, that's fine
    }

    // Parse [[title]] links from content
    const linkRegex = /\[\[([^\]]+)\]\]/g;
    const linkedTitles = new Set<string>();
    let match;
    while ((match = linkRegex.exec(content)) !== null) {
      linkedTitles.add(match[1]);
    }

    // Create new LINKS_TO edges for each matched title
    for (const linkedTitle of Array.from(linkedTitles)) {
      const safeLinked = linkedTitle.replace(/"/g, '\\"');
      try {
        await query(
          `MATCH (a), (b:Note) WHERE id(a) = ${id} AND b.title = "${safeLinked}" CREATE (a)-[:LINKS_TO]->(b)`
        );
      } catch {
        // Target note doesn't exist yet, skip
      }
    }

    // Return updated note
    const result = await query(`MATCH (n) WHERE id(n) = ${id} RETURN n`);
    return NextResponse.json(nodeToNote(result.rows[0].n as NeoNode));
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}

export async function DELETE(
  _request: Request,
  { params }: { params: { id: string } }
) {
  try {
    const id = parseInt(params.id, 10);

    // Delete outgoing edges
    try {
      const outgoing = await query(
        `MATCH (n)-[e:LINKS_TO]->(m) WHERE id(n) = ${id} RETURN e, m`
      );
      for (const row of outgoing.rows) {
        const target = row.m as NeoNode;
        await query(
          `MATCH (a)-[e:LINKS_TO]->(b) WHERE id(a) = ${id} AND id(b) = ${target.id} DELETE e`
        );
      }
    } catch {
      // No outgoing links
    }

    // Delete incoming edges
    try {
      const incoming = await query(
        `MATCH (m)-[e:LINKS_TO]->(n) WHERE id(n) = ${id} RETURN e, m`
      );
      for (const row of incoming.rows) {
        const source = row.m as NeoNode;
        await query(
          `MATCH (a)-[e:LINKS_TO]->(b) WHERE id(a) = ${source.id} AND id(b) = ${id} DELETE e`
        );
      }
    } catch {
      // No incoming links
    }

    // Delete the node
    await query(`MATCH (n) WHERE id(n) = ${id} DELETE n`);
    return NextResponse.json({ ok: true });
  } catch (err) {
    return NextResponse.json(
      { error: (err as Error).message },
      { status: 500 }
    );
  }
}
