"use client";

import React from "react";
import {
  Code,
  Database,
  Globe,
  Wrench,
  ShieldCheck,
  Cpu,
  Workflow,
  Sparkles,
  Layers,
  Coins,
  Search,
  Terminal,
  Box
} from "lucide-react";

export const CATEGORIES = [
  { id: "all", label: "All Categories", icon: Layers },
  { id: "Official Core", label: "Official Core", icon: ShieldCheck },
  { id: "Developer Tools", label: "Developer Tools", icon: Code },
  { id: "Databases", label: "Databases", icon: Database },
  { id: "Browser Automation", label: "Browser Automation", icon: Globe },
  { id: "Productivity & Workflow", label: "Productivity & Workflow", icon: Sparkles },
  { id: "Agent Skills", label: "Agent Skills", icon: Wrench },
  { id: "Cloud Infrastructure", label: "Cloud Infrastructure", icon: Cpu },
  { id: "Security & Testing", label: "Security & Testing", icon: ShieldCheck },
  { id: "Finance & Crypto", label: "Finance & Crypto", icon: Coins },
  { id: "Search & Retrieval", label: "Search & Retrieval", icon: Search },
  { id: "Plugins & Toolkits", label: "Plugins & Toolkits", icon: Box },
];

interface CategoryRailProps {
  selectedCategory: string;
  onSelectCategory: (cat: string) => void;
}

export function CategoryRail({ selectedCategory, onSelectCategory }: CategoryRailProps) {
  return (
    <div className="w-full max-w-6xl mx-auto px-4 mb-8">
      <div className="category-rail-mask overflow-x-auto no-scrollbar py-2 overscroll-x-contain snap-x snap-mandatory">
        <div className="flex items-center gap-2 min-w-max px-4">
          {CATEGORIES.map((cat) => {
            const Icon = cat.icon;
            const isSelected = selectedCategory === cat.id;

            return (
              <button
                key={cat.id}
                onClick={() => onSelectCategory(cat.id)}
                className={`flex items-center gap-2 px-3.5 py-2 rounded-xl text-xs font-semibold border transition-all snap-start ${
                  isSelected
                    ? "bg-slate-900 border-slate-900 text-white shadow-sm"
                    : "bg-white border-slate-200/90 text-slate-600 hover:text-slate-900 hover:border-slate-300 shadow-sm"
                }`}
              >
                <Icon className={`w-3.5 h-3.5 ${isSelected ? "text-emerald-400" : "text-slate-400"}`} />
                <span>{cat.label}</span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
