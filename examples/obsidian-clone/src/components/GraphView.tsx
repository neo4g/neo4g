"use client";

import { useEffect, useRef, useCallback } from "react";
import { useRouter } from "next/navigation";

interface GraphNode {
  id: number;
  title: string;
  x: number;
  y: number;
  vx: number;
  vy: number;
}

interface GraphEdge {
  source: number;
  target: number;
}

interface GraphViewProps {
  nodes: { id: number; title: string }[];
  edges: { source: number; target: number }[];
}

export default function GraphView({ nodes: rawNodes, edges }: GraphViewProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const nodesRef = useRef<GraphNode[]>([]);
  const animRef = useRef<number>(0);
  const dragRef = useRef<{ node: GraphNode; offsetX: number; offsetY: number } | null>(null);
  const didDragRef = useRef(false);
  const hoveredRef = useRef<GraphNode | null>(null);
  const router = useRouter();

  // Initialize node positions in a circle
  useEffect(() => {
    const cx = 0;
    const cy = 0;
    const radius = Math.max(150, rawNodes.length * 30);

    nodesRef.current = rawNodes.map((n, i) => {
      const angle = (2 * Math.PI * i) / Math.max(rawNodes.length, 1);
      return {
        ...n,
        x: cx + radius * Math.cos(angle) + (Math.random() - 0.5) * 20,
        y: cy + radius * Math.sin(angle) + (Math.random() - 0.5) * 20,
        vx: 0,
        vy: 0,
      };
    });
  }, [rawNodes]);

  const simulate = useCallback(() => {
    const nodes = nodesRef.current;
    if (nodes.length === 0) return;

    const damping = 0.85;
    const repulsion = 3000;
    const attraction = 0.005;
    const centerPull = 0.01;

    // Forces
    for (let i = 0; i < nodes.length; i++) {
      const a = nodes[i];
      if (dragRef.current?.node.id === a.id) continue;

      let fx = 0;
      let fy = 0;

      // Repulsion from all other nodes
      for (let j = 0; j < nodes.length; j++) {
        if (i === j) continue;
        const b = nodes[j];
        const dx = a.x - b.x;
        const dy = a.y - b.y;
        const dist = Math.max(Math.sqrt(dx * dx + dy * dy), 1);
        const force = repulsion / (dist * dist);
        fx += (dx / dist) * force;
        fy += (dy / dist) * force;
      }

      // Attraction along edges
      for (const edge of edges) {
        let other: GraphNode | undefined;
        if (edge.source === a.id) other = nodes.find((n) => n.id === edge.target);
        else if (edge.target === a.id) other = nodes.find((n) => n.id === edge.source);
        if (!other) continue;

        const dx = other.x - a.x;
        const dy = other.y - a.y;
        fx += dx * attraction;
        fy += dy * attraction;
      }

      // Center pull
      fx -= a.x * centerPull;
      fy -= a.y * centerPull;

      a.vx = (a.vx + fx) * damping;
      a.vy = (a.vy + fy) * damping;
      a.x += a.vx;
      a.y += a.vy;
    }
  }, [edges]);

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const rect = canvas.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    canvas.width = rect.width * dpr;
    canvas.height = rect.height * dpr;
    ctx.scale(dpr, dpr);

    const w = rect.width;
    const h = rect.height;
    const cx = w / 2;
    const cy = h / 2;

    ctx.clearRect(0, 0, w, h);

    const nodes = nodesRef.current;
    const hovered = hoveredRef.current;

    // Draw edges
    for (const edge of edges) {
      const from = nodes.find((n) => n.id === edge.source);
      const to = nodes.find((n) => n.id === edge.target);
      if (!from || !to) continue;

      const isHighlighted =
        hovered && (hovered.id === from.id || hovered.id === to.id);

      ctx.beginPath();
      ctx.moveTo(from.x + cx, from.y + cy);
      ctx.lineTo(to.x + cx, to.y + cy);
      ctx.strokeStyle = isHighlighted
        ? "rgba(124, 106, 247, 0.6)"
        : "rgba(61, 61, 61, 0.8)";
      ctx.lineWidth = isHighlighted ? 2 : 1;
      ctx.stroke();

      // Arrow head
      const angle = Math.atan2(to.y - from.y, to.x - from.x);
      const arrowLen = 8;
      const tx = to.x + cx - Math.cos(angle) * 14;
      const ty = to.y + cy - Math.sin(angle) * 14;
      ctx.beginPath();
      ctx.moveTo(tx, ty);
      ctx.lineTo(
        tx - arrowLen * Math.cos(angle - 0.4),
        ty - arrowLen * Math.sin(angle - 0.4)
      );
      ctx.lineTo(
        tx - arrowLen * Math.cos(angle + 0.4),
        ty - arrowLen * Math.sin(angle + 0.4)
      );
      ctx.closePath();
      ctx.fillStyle = isHighlighted
        ? "rgba(124, 106, 247, 0.6)"
        : "rgba(61, 61, 61, 0.8)";
      ctx.fill();
    }

    // Draw nodes
    for (const node of nodes) {
      const isHovered = hovered?.id === node.id;
      const radius = isHovered ? 10 : 7;

      // Glow
      if (isHovered) {
        ctx.beginPath();
        ctx.arc(node.x + cx, node.y + cy, 18, 0, 2 * Math.PI);
        ctx.fillStyle = "rgba(124, 106, 247, 0.1)";
        ctx.fill();
      }

      // Circle
      ctx.beginPath();
      ctx.arc(node.x + cx, node.y + cy, radius, 0, 2 * Math.PI);
      ctx.fillStyle = isHovered ? "#9580ff" : "#7c6af7";
      ctx.fill();
      ctx.strokeStyle = isHovered ? "#b8a9ff" : "#5a4fcf";
      ctx.lineWidth = 1.5;
      ctx.stroke();

      // Label
      ctx.font = `${isHovered ? "13px" : "11px"} -apple-system, system-ui, sans-serif`;
      ctx.fillStyle = isHovered ? "#dcddde" : "#888888";
      ctx.textAlign = "center";
      ctx.fillText(node.title, node.x + cx, node.y + cy + radius + 14);
    }
  }, [edges]);

  // Animation loop
  useEffect(() => {
    const tick = () => {
      simulate();
      draw();
      animRef.current = requestAnimationFrame(tick);
    };
    animRef.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(animRef.current);
  }, [simulate, draw]);

  // Mouse interactions
  const getNodeAt = useCallback(
    (mx: number, my: number): GraphNode | null => {
      const canvas = canvasRef.current;
      if (!canvas) return null;
      const rect = canvas.getBoundingClientRect();
      const cx = rect.width / 2;
      const cy = rect.height / 2;
      const x = mx - rect.left - cx;
      const y = my - rect.top - cy;

      for (const node of nodesRef.current) {
        const dx = node.x - x;
        const dy = node.y - y;
        if (dx * dx + dy * dy < 15 * 15) return node;
      }
      return null;
    },
    []
  );

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      const node = getNodeAt(e.clientX, e.clientY);
      if (node) {
        const canvas = canvasRef.current!;
        const rect = canvas.getBoundingClientRect();
        dragRef.current = {
          node,
          offsetX: e.clientX - rect.left - rect.width / 2 - node.x,
          offsetY: e.clientY - rect.top - rect.height / 2 - node.y,
        };
        didDragRef.current = false;
      }
    },
    [getNodeAt]
  );

  const handleMouseMove = useCallback(
    (e: React.MouseEvent) => {
      if (dragRef.current) {
        didDragRef.current = true;
        const canvas = canvasRef.current!;
        const rect = canvas.getBoundingClientRect();
        dragRef.current.node.x =
          e.clientX - rect.left - rect.width / 2 - dragRef.current.offsetX;
        dragRef.current.node.y =
          e.clientY - rect.top - rect.height / 2 - dragRef.current.offsetY;
        dragRef.current.node.vx = 0;
        dragRef.current.node.vy = 0;
      } else {
        const node = getNodeAt(e.clientX, e.clientY);
        hoveredRef.current = node;
        const canvas = canvasRef.current;
        if (canvas) canvas.style.cursor = node ? "pointer" : "default";
      }
    },
    [getNodeAt]
  );

  const handleMouseUp = useCallback(() => {
    dragRef.current = null;
  }, []);

  const handleClick = useCallback(
    (e: React.MouseEvent) => {
      if (didDragRef.current) return;
      const node = getNodeAt(e.clientX, e.clientY);
      if (node) {
        router.push(`/notes/${node.id}`);
      }
    },
    [getNodeAt, router]
  );

  return (
    <canvas
      ref={canvasRef}
      className="w-full h-full"
      onMouseDown={handleMouseDown}
      onMouseMove={handleMouseMove}
      onMouseUp={handleMouseUp}
      onClick={handleClick}
    />
  );
}
