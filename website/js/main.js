/**
 * Self-Healing Distributed Cache - Portfolio Website
 * Main JavaScript - interactions, animations, and UI logic
 */

(function () {
    'use strict';

    // ==========================================
    // DOM READY
    // ==========================================
    document.addEventListener('DOMContentLoaded', function () {
        initMobileNav();
        initScrollHeader();
        initSmoothScroll();
        initScrollAnimations();
        initStatsCounter();
        initActiveNav();
    });

    // ==========================================
    // MOBILE NAVIGATION
    // ==========================================
    function initMobileNav() {
        const hamburger = document.getElementById('hamburger');
        const nav = document.getElementById('nav');

        if (!hamburger || !nav) return;

        hamburger.addEventListener('click', function () {
            hamburger.classList.toggle('active');
            nav.classList.toggle('active');
        });

        // Close nav when a link is clicked
        nav.querySelectorAll('.nav-link').forEach(function (link) {
            link.addEventListener('click', function () {
                hamburger.classList.remove('active');
                nav.classList.remove('active');
            });
        });

        // Close nav when clicking outside
        document.addEventListener('click', function (e) {
            if (!hamburger.contains(e.target) && !nav.contains(e.target)) {
                hamburger.classList.remove('active');
                nav.classList.remove('active');
            }
        });
    }

    // ==========================================
    // STICKY HEADER SCROLL EFFECT
    // ==========================================
    function initScrollHeader() {
        const header = document.getElementById('header');
        if (!header) return;

        var scrollThreshold = 20;

        function onScroll() {
            if (window.scrollY > scrollThreshold) {
                header.classList.add('scrolled');
            } else {
                header.classList.remove('scrolled');
            }
        }

        window.addEventListener('scroll', onScroll, { passive: true });
        onScroll(); // Check initial state
    }

    // ==========================================
    // SMOOTH SCROLL FOR NAV LINKS
    // ==========================================
    function initSmoothScroll() {
        document.querySelectorAll('a[href^="#"]').forEach(function (anchor) {
            anchor.addEventListener('click', function (e) {
                var targetId = this.getAttribute('href');
                if (targetId === '#') return;

                var target = document.querySelector(targetId);
                if (!target) return;

                e.preventDefault();
                var headerHeight = document.getElementById('header').offsetHeight || 0;
                var targetPosition = target.getBoundingClientRect().top + window.pageYOffset - headerHeight - 16;

                window.scrollTo({
                    top: targetPosition,
                    behavior: 'smooth'
                });
            });
        });
    }

    // ==========================================
    // SCROLL ANIMATIONS (FADE IN)
    // ==========================================
    function initScrollAnimations() {
        // Elements to animate on scroll
        var animatedElements = document.querySelectorAll('.section-header, .feature-card, .arch-step, .tech-badge, .process-step, .doc-card, .stat-item');

        animatedElements.forEach(function (el, index) {
            el.classList.add('fade-in');
            // Stagger the animation delay
            el.style.transitionDelay = (Math.min(index % 4, 3) * 0.1) + 's';
        });

        // Intersection Observer for fade-in
        if ('IntersectionObserver' in window) {
            var observerOptions = {
                root: null,
                rootMargin: '0px 0px -60px 0px',
                threshold: 0.1
            };

            var observer = new IntersectionObserver(function (entries) {
                entries.forEach(function (entry) {
                    if (entry.isIntersecting) {
                        entry.target.classList.add('visible');
                        observer.unobserve(entry.target);
                    }
                });
            }, observerOptions);

            animatedElements.forEach(function (el) {
                observer.observe(el);
            });
        } else {
            // Fallback: just show everything
            animatedElements.forEach(function (el) {
                el.classList.add('visible');
            });
        }
    }

    // ==========================================
    // STATS COUNTER ANIMATION
    // ==========================================
    function initStatsCounter() {
        var statItems = document.querySelectorAll('.stat-item[data-count]');
        if (!statItems.length) return;

        function animateCount(el, target) {
            var duration = 1500;
            var start = 0;
            var startTime = null;

            function update(timestamp) {
                if (!startTime) startTime = timestamp;
                var progress = Math.min((timestamp - startTime) / duration, 1);
                // Ease out quad
                var eased = 1 - (1 - progress) * (1 - progress);
                var current = Math.round(start + (target - start) * eased);
                el.textContent = current;
                if (progress < 1) {
                    requestAnimationFrame(update);
                } else {
                    el.textContent = target;
                }
            }

            requestAnimationFrame(update);
        }

        if ('IntersectionObserver' in window) {
            var statsObserver = new IntersectionObserver(function (entries) {
                entries.forEach(function (entry) {
                    if (entry.isIntersecting) {
                        var target = parseInt(entry.target.getAttribute('data-count'), 10);
                        var numEl = entry.target.querySelector('.stat-number');
                        if (numEl && target) {
                            animateCount(numEl, target);
                        }
                        statsObserver.unobserve(entry.target);
                    }
                });
            }, { threshold: 0.5 });

            statItems.forEach(function (item) {
                statsObserver.observe(item);
            });
        }
    }

    // ==========================================
    // ACTIVE NAV LINK HIGHLIGHT
    // ==========================================
    function initActiveNav() {
        var sections = document.querySelectorAll('section[id]');
        var navLinks = document.querySelectorAll('.nav-link');
        if (!sections.length || !navLinks.length) return;

        function highlightNav() {
            var scrollY = window.scrollY;
            var headerHeight = document.getElementById('header').offsetHeight || 0;
            var current = '';

            sections.forEach(function (section) {
                var sectionTop = section.offsetTop - headerHeight - 100;
                var sectionHeight = section.offsetHeight;
                if (scrollY >= sectionTop && scrollY < sectionTop + sectionHeight) {
                    current = section.getAttribute('id');
                }
            });

            navLinks.forEach(function (link) {
                link.classList.remove('active');
                if (link.getAttribute('href') === '#' + current) {
                    link.classList.add('active');
                }
            });
        }

        window.addEventListener('scroll', highlightNav, { passive: true });
        highlightNav(); // Check initial state
    }

})();
