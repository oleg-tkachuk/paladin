'use client';

import React from 'react';
import { cn } from '@/lib/utils';

interface SparklineProps {
    data: number[];
    className?: string;
    color?: string;
}

export const Sparkline = React.memo(({ data, className, color = 'indigo' }: SparklineProps) => {
    if (!data.length) return null;

    const max = Math.max(...data);
    const min = Math.min(...data);
    const range = max - min || 1;
    const width = 100;
    const height = 30;

    const points = data.map((d, i) => ({
        x: (i / (data.length - 1)) * width,
        y: height - ((d - min) / range) * height
    }));

    const pathData = `M ${points.map(p => `${p.x},${p.y}`).join(' L ')}`;
    const areaData = `${pathData} L ${width},${height} L 0,${height} Z`;

    const colorVariants: Record<string, { stroke: string; fill: string; gradient: string }> = {
        indigo: { stroke: '#6366f1', fill: 'rgba(99, 102, 241, 0.1)', gradient: 'indigo-gradient' },
        emerald: { stroke: '#10b981', fill: 'rgba(16, 185, 129, 0.1)', gradient: 'emerald-gradient' },
        blue: { stroke: '#3b82f6', fill: 'rgba(59, 130, 246, 0.1)', gradient: 'blue-gradient' },
        rose: { stroke: '#f43f5e', fill: 'rgba(244, 63, 94, 0.1)', gradient: 'rose-gradient' },
    };

    const v = colorVariants[color] || colorVariants.indigo;

    return (
        <svg
            viewBox={`0 0 ${width} ${height}`}
            className={cn("w-full h-10 overflow-visible", className)}
            preserveAspectRatio="none"
        >
            <defs>
                <linearGradient id={v.gradient} x1="0" x2="0" y1="0" y2="1">
                    <stop offset="0%" stopColor={v.stroke} stopOpacity="0.2" />
                    <stop offset="100%" stopColor={v.stroke} stopOpacity="0" />
                </linearGradient>
            </defs>
            <path
                d={areaData}
                fill={`url(#${v.gradient})`}
                className="transition-all duration-1000"
            />
            <path
                d={pathData}
                fill="none"
                stroke={v.stroke}
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className="transition-all duration-1000"
            />
        </svg>
    );
});

Sparkline.displayName = 'Sparkline';
