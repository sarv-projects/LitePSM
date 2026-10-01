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
} from "lucide-react";

export const CATEGORIES = [
  { id: "all", label: "All Categories", icon: Layers },
  { id: "Developer Tools", label: "Developer Tools", icon: Code },
  { id: "Database Management", label: "Database Management", icon: Database },
  { id: "Browser Automation", label: "Browser Automation", icon: Globe },
  { id: "DevOps", label: "DevOps & Cloud", icon: Workflow },
  { id: "Security & Testing", label: "Security & Testing", icon: ShieldCheck },
  { id: "Productivity", label: "Productivity", icon: Sparkles },
  { id: "Data Science & ML", label: "Data Science & ML", icon: Cpu },
];

interface CategoryRailProps {
  selectedCategory: string;
  onSelectCategory: (cat: string) => void;
}

export function CategoryRail({ selectedCategory, onSelectCategory }: CategoryRailProps) {
  return (
    <div className="w-full max-w-6xl mx-auto px-4 mb-8 overflow-x-auto no-scrollbar py-2 overscroll-x-contain snap-x snap-mandatory">
      <div className="flex items-center gap-2 min-w-max justify-start md:justify-center">
        {CATEGORIES.map((cat) => {
          const Icon = cat.icon;
          const isSelected = selectedCategory === cat.id;

          return (
            <button
              key={cat.id}
              onClick={() => onSelectCategory(cat.id)}
              className={`flex items-center gap-2 px-3.5 py-2 rounded-xl text-xs font-medium border transition-all snap-start ${
                isSelected
                  ? "bg-emerald-500/10 border-emerald-500/50 text-emerald-400 shadow-sm shadow-emerald-950"
                  : "glass-panel border-[#232734] text-gray-400 hover:text-gray-200 hover:border-gray-700"
              }`}
            >
              <Icon className={`w-3.5 h-3.5 ${isSelected ? "text-emerald-400" : "text-gray-400"}`} />
              <span>{cat.label}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
