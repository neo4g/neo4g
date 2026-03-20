const NEO4G_URL = process.env.NEO4G_URL || "http://localhost:7474";

export interface NeoNode {
  id: number;
  labels: string[];
  properties: Record<string, unknown>;
}

export interface Note {
  id: number;
  title: string;
  content: string;
  createdAt: string;
}

export function nodeToNote(node: NeoNode): Note {
  return {
    id: node.id,
    title: (node.properties.title as string) || "Untitled",
    content: (node.properties.content as string) || "",
    createdAt: (node.properties.createdAt as string) || "",
  };
}

export async function query(
  cypher: string,
  params: Record<string, unknown> = {}
): Promise<{ columns: string[]; rows: Record<string, unknown>[] }> {
  const res = await fetch(`${NEO4G_URL}/api/query`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query: cypher, params }),
    cache: "no-store",
  });
  const json = await res.json();
  if (json.error) throw new Error(json.error);
  return json.data;
}
